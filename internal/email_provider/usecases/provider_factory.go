package usecases

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/gsmail/imap"
	"github.com/gsoultan/gsmail/pop3"
	"github.com/gsoultan/gsmail/smtp"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
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
	mu      sync.Mutex
	senders map[string]*smtp.Sender
}

func NewProviderFactory() entities.ProviderFactory {
	return &providerFactory{senders: make(map[string]*smtp.Sender)}
}

// CreateSender returns a pooled sender for the provider.
//
// Senders are cached per provider and per configuration: editing a provider
// produces a different fingerprint, so the old sender is replaced rather than
// silently kept with stale credentials.
func (f *providerFactory) CreateSender(p *entities.EmailProvider) (gsmail.Sender, error) {
	if p.Type != panmailv1.ProviderType_PROVIDER_TYPE_SMTP {
		return nil, fmt.Errorf("provider type %v does not support sending", p.Type)
	}

	c := &panmailv1.SmtpConfig{}
	if err := protojson.Unmarshal(p.Config, c); err != nil {
		return nil, err
	}

	key := senderCacheKey(p.ID, p.Config)

	f.mu.Lock()
	defer f.mu.Unlock()

	if sender, ok := f.senders[key]; ok {
		return sender, nil
	}

	// The configuration changed: retire every sender for this provider so the
	// old connections, and the credentials on them, are not reused.
	f.closeProviderLocked(p.ID)

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

	sender.EnablePool(senderPoolConfig)

	f.senders[key] = sender
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
		_ = sender.Close()
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
			_ = sender.Close()
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
