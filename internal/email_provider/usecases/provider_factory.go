package usecases

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/gsmail/imap"
	"github.com/gsoultan/gsmail/mailgun"
	"github.com/gsoultan/gsmail/otelgs"
	"github.com/gsoultan/gsmail/pop3"
	"github.com/gsoultan/gsmail/postmark"
	"github.com/gsoultan/gsmail/sendgrid"
	"github.com/gsoultan/gsmail/ses"
	"github.com/gsoultan/gsmail/smtp"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/pkg/oauth2"
	"google.golang.org/protobuf/encoding/protojson"
)

// Pool sizing. A fresh sender per recipient means a TCP connect, a TLS
// handshake and an AUTH round trip for every single address, which is what
// held throughput down; reusing connections removes all three from the common
// path.
var senderPoolConfig = smtp.PoolConfig{
	MaxIdle:     4,
	MaxOpen:     16,
	IdleTimeout: 30 * time.Second,
	MaxLifetime: 5 * time.Minute,
	Wait:        true,
}

type providerFactory struct {
	mu sync.Mutex
	// Typed as the interface, not *smtp.Sender, because the API-backed senders
	// are also cached here. Only SMTP holds connections; the others are HTTP
	// clients with nothing to close, which is why closing is conditional.
	senders map[string]gsmail.Sender
}

func NewProviderFactory() entities.ProviderFactory {
	return &providerFactory{senders: make(map[string]gsmail.Sender)}
}

// closeSender releases a sender's resources if it has any. Only the SMTP sender
// pools connections; the API senders implement no Close.
func closeSender(s gsmail.Sender) {
	if c, ok := s.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

// CreateSender returns a pooled sender for the provider.
//
// Senders are cached per provider and per configuration: editing a provider
// produces a different fingerprint, so the old sender is replaced rather than
// silently kept with stale credentials.
func (f *providerFactory) CreateSender(p *entities.EmailProvider) (gsmail.Sender, error) {
	key := senderCacheKey(p.ID, p.Config)

	f.mu.Lock()
	defer f.mu.Unlock()

	if sender, ok := f.senders[key]; ok {
		return observed(sender), nil
	}

	// The configuration changed: retire every sender for this provider so the
	// old connections, and the credentials on them, are not reused.
	f.closeProviderLocked(p.ID)

	sender, err := buildSender(p)
	if err != nil {
		return nil, err
	}

	// The raw sender is what goes in the cache, because that is what owns the
	// connection pool and implements Close. Wrapping happens on the way out:
	// an interceptor chain has no Close, so caching the wrapper would leak
	// every pooled SMTP connection at shutdown.
	f.senders[key] = sender
	return observed(sender), nil
}

// observed adds the cross-cutting behaviour every send should have.
//
// Recovery first, so a panic inside a vendor SDK becomes an error on one
// message rather than taking down the worker that happened to be draining the
// outbox at the time.
//
// The OpenTelemetry interceptors are attached unconditionally. With no provider
// configured, OTel resolves to a no-op and costs almost nothing; the moment an
// operator points the process at a collector, send spans and counters appear
// without a code change. They deliberately record no addresses or subjects.
func observed(s gsmail.Sender) gsmail.Sender {
	return gsmail.WrapSender(s,
		gsmail.RecoveryInterceptor(),
		otelgs.SendInterceptor(),
		otelgs.SendMetricsInterceptor(),
	)
}

// buildSender constructs the sender for a provider type.
//
// Separate from the caching so the construction is testable on its own, and so
// adding a provider is one case rather than a change to the locking.
func buildSender(p *entities.EmailProvider) (gsmail.Sender, error) {
	switch p.Type {
	case panmailv1.ProviderType_PROVIDER_TYPE_SMTP:
		return buildSMTPSender(p)

	case panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID:
		c := &panmailv1.SendGridConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		if c.GetApiKey() == "" {
			return nil, fmt.Errorf("sendgrid provider %q has no API key", p.Name)
		}
		s := sendgrid.NewSender(c.GetApiKey())
		if c.GetBaseUrl() != "" {
			s.BaseURL = c.GetBaseUrl()
		}
		return s, nil

	case panmailv1.ProviderType_PROVIDER_TYPE_SES:
		c := &panmailv1.SesConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		if c.GetRegion() == "" || c.GetAccessKey() == "" || c.GetSecretKey() == "" {
			return nil, fmt.Errorf("ses provider %q needs a region, access key and secret key", p.Name)
		}
		return ses.NewSender(c.GetRegion(), c.GetAccessKey(), c.GetSecretKey(), c.GetEndpoint()), nil

	case panmailv1.ProviderType_PROVIDER_TYPE_POSTMARK:
		c := &panmailv1.PostmarkConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		if c.GetServerToken() == "" {
			return nil, fmt.Errorf("postmark provider %q has no server token", p.Name)
		}
		s := postmark.NewSender(c.GetServerToken())
		// Postmark rejects bulk mail on a transactional stream, so a campaign
		// that leaves this empty fails at the API rather than being delivered.
		s.MessageStream = c.GetMessageStream()
		if c.GetBaseUrl() != "" {
			s.BaseURL = c.GetBaseUrl()
		}
		return s, nil

	case panmailv1.ProviderType_PROVIDER_TYPE_MAILGUN:
		c := &panmailv1.MailgunConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		if c.GetDomain() == "" || c.GetApiKey() == "" {
			return nil, fmt.Errorf("mailgun provider %q needs a domain and an API key", p.Name)
		}
		s := mailgun.NewSender(c.GetDomain(), c.GetApiKey())
		if c.GetBaseUrl() != "" {
			s.BaseURL = c.GetBaseUrl()
		}
		return s, nil

	default:
		return nil, fmt.Errorf("provider type %v does not support sending", p.Type)
	}
}

func buildSMTPSender(p *entities.EmailProvider) (gsmail.Sender, error) {
	c := &panmailv1.SmtpConfig{}
	if err := protojson.Unmarshal(p.Config, c); err != nil {
		return nil, err
	}

	sender := smtp.NewSender(c.Host, int(c.Port), c.Username, c.Password, c.UseSsl)
	sender.InsecureSkipVerify = c.SkipVerify

	// Signing is enabled only when all three parts are present. A partial
	// configuration would make every send fail at signing time, which is a
	// worse outcome than sending unsigned: unsigned mail is delivered and
	// merely distrusted, while a signing error stops delivery entirely.
	if d := c.GetDkim(); d.GetDomain() != "" && d.GetSelector() != "" && d.GetPrivateKey() != "" {
		sender.DKIMConfig = &gsmail.DKIMOptions{
			Domain:     d.GetDomain(),
			Selector:   d.GetSelector(),
			PrivateKey: d.GetPrivateKey(),
		}
	}

	// OAuth2 instead of a password. gsmail's CachingTokenSource fetches a token
	// once and reuses it until shortly before expiry, single-flighting
	// concurrent refreshes — without it, TokenSource is called on every send and
	// every retry, which is a round trip to the identity provider per message.
	if o := c.GetOauth2(); o.GetClientId() != "" && o.GetRefreshToken() != "" && o.GetTokenEndpoint() != "" {
		method := gsmail.AuthXOAUTH2
		if strings.EqualFold(o.GetMechanism(), string(gsmail.AuthOAUTHBEARER)) {
			method = gsmail.AuthOAUTHBEARER
		}
		sender.UseOAuth(method, gsmail.CachingTokenSource(
			oauth2.RefreshFunc(oauth2.Config{
				TokenEndpoint: o.GetTokenEndpoint(),
				ClientID:      o.GetClientId(),
				ClientSecret:  o.GetClientSecret(),
				RefreshToken:  o.GetRefreshToken(),
				Scope:         o.GetScope(),
			}),
			0, // zero uses gsmail's default leeway
		))
	}

	sender.EnablePool(senderPoolConfig)
	return sender, nil
}

func (f *providerFactory) CreateReceiver(p *entities.EmailProvider) (gsmail.Receiver, error) {
	switch p.Type {
	case panmailv1.ProviderType_PROVIDER_TYPE_IMAP:
		c := &panmailv1.ImapConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		r := imap.NewReceiver(c.Host, int(c.Port), c.Username, c.Password, c.UseSsl)
		r.InsecureSkipVerify = c.SkipVerify
		return r, nil
	case panmailv1.ProviderType_PROVIDER_TYPE_POP3:
		c := &panmailv1.Pop3Config{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		r := pop3.NewReceiver(c.Host, int(c.Port), c.Username, c.Password, c.UseSsl)
		r.InsecureSkipVerify = c.SkipVerify
		return r, nil
	default:
		return nil, fmt.Errorf("provider type %v does not support receiving", p.Type)
	}
}

// Close shuts every pooled connection down. Called during shutdown so that
// sockets are not left open behind the process.
func (f *providerFactory) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for key, sender := range f.senders {
		closeSender(sender)
		delete(f.senders, key)
	}
	return nil
}

// closeProviderLocked retires cached senders for one provider. The caller must
// hold the lock.
func (f *providerFactory) closeProviderLocked(providerID string) {
	prefix := providerID + ":"
	for key, sender := range f.senders {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			closeSender(sender)
			delete(f.senders, key)
		}
	}
}

// senderCacheKey fingerprints a provider's configuration so a credential
// change invalidates the cached sender.
func senderCacheKey(providerID string, config []byte) string {
	sum := sha256.Sum256(config)
	return providerID + ":" + hex.EncodeToString(sum[:8])
}
