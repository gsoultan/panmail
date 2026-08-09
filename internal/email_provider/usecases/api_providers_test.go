package usecases

import (
	"strings"
	"testing"

	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/gsmail/mailgun"
	"github.com/gsoultan/gsmail/postmark"
	"github.com/gsoultan/gsmail/sendgrid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func providerWith(t *testing.T, typ panmailv1.ProviderType, cfg proto.Message) *entities.EmailProvider {
	t.Helper()
	b, err := protojson.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &entities.EmailProvider{ID: "p1", Name: "test", Type: typ, Config: b}
}

// Each API provider must actually build a sender, or the type is present in the
// enum and the UI while every send fails at run time — which is the state these
// four were in before: declared, then reserved out because nothing implemented
// them.
func TestEveryApiProviderBuildsASender(t *testing.T) {
	cases := []struct {
		name string
		typ  panmailv1.ProviderType
		cfg  proto.Message
	}{
		{"sendgrid", panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID,
			&panmailv1.SendGridConfig{ApiKey: "SG.key"}},
		{"ses", panmailv1.ProviderType_PROVIDER_TYPE_SES,
			&panmailv1.SesConfig{Region: "us-east-1", AccessKey: "AKIA", SecretKey: "secret"}},
		{"postmark", panmailv1.ProviderType_PROVIDER_TYPE_POSTMARK,
			&panmailv1.PostmarkConfig{ServerToken: "token"}},
		{"mailgun", panmailv1.ProviderType_PROVIDER_TYPE_MAILGUN,
			&panmailv1.MailgunConfig{Domain: "mg.example.com", ApiKey: "key"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender, err := buildSender(providerWith(t, tc.typ, tc.cfg))
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if sender == nil {
				t.Fatal("expected a sender")
			}
		})
	}
}

// A provider missing its credential must fail where it is built, not on the
// first send. Discovering it at send time means a queued message and a failed
// delivery for something the operator could have been told immediately.
func TestAnIncompleteApiProviderIsRefused(t *testing.T) {
	cases := []struct {
		name string
		typ  panmailv1.ProviderType
		cfg  proto.Message
		want string
	}{
		{"sendgrid without a key", panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID,
			&panmailv1.SendGridConfig{}, "API key"},
		{"ses without a secret", panmailv1.ProviderType_PROVIDER_TYPE_SES,
			&panmailv1.SesConfig{Region: "us-east-1", AccessKey: "AKIA"}, "secret key"},
		{"ses without a region", panmailv1.ProviderType_PROVIDER_TYPE_SES,
			&panmailv1.SesConfig{AccessKey: "AKIA", SecretKey: "s"}, "region"},
		{"postmark without a token", panmailv1.ProviderType_PROVIDER_TYPE_POSTMARK,
			&panmailv1.PostmarkConfig{}, "server token"},
		{"mailgun without a domain", panmailv1.ProviderType_PROVIDER_TYPE_MAILGUN,
			&panmailv1.MailgunConfig{ApiKey: "key"}, "domain"},
		{"mailgun without a key", panmailv1.ProviderType_PROVIDER_TYPE_MAILGUN,
			&panmailv1.MailgunConfig{Domain: "mg.example.com"}, "API key"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildSender(providerWith(t, tc.typ, tc.cfg))
			if err == nil {
				t.Fatal("expected the provider to be refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error should name what is missing (%q), got: %v", tc.want, err)
			}
		})
	}
}

// A receive-only provider has no sender, and asking for one must say so rather
// than returning something that fails later.
func TestReceiveOnlyProvidersHaveNoSender(t *testing.T) {
	for _, typ := range []panmailv1.ProviderType{
		panmailv1.ProviderType_PROVIDER_TYPE_IMAP,
		panmailv1.ProviderType_PROVIDER_TYPE_POP3,
		panmailv1.ProviderType_PROVIDER_TYPE_UNSPECIFIED,
	} {
		if _, err := buildSender(providerWith(t, typ, &panmailv1.ImapConfig{Host: "h"})); err == nil {
			t.Errorf("%v should not produce a sender", typ)
		}
	}
}

// Optional overrides exist for regional endpoints and test doubles. Mailgun's EU
// region in particular uses a different host, and sending to the wrong one fails
// authentication rather than falling back.
func TestBaseURLOverridesAreApplied(t *testing.T) {
	sg, err := buildSender(providerWith(t, panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID,
		&panmailv1.SendGridConfig{ApiKey: "k", BaseUrl: "https://eu.api.sendgrid.com"}))
	if err != nil {
		t.Fatalf("sendgrid: %v", err)
	}
	if got := sg.(*sendgrid.Sender).BaseURL; got != "https://eu.api.sendgrid.com" {
		t.Errorf("sendgrid base URL = %q", got)
	}

	mg, err := buildSender(providerWith(t, panmailv1.ProviderType_PROVIDER_TYPE_MAILGUN,
		&panmailv1.MailgunConfig{Domain: "d", ApiKey: "k", BaseUrl: "https://api.eu.mailgun.net/v3"}))
	if err != nil {
		t.Fatalf("mailgun: %v", err)
	}
	if got := mg.(*mailgun.Sender).BaseURL; got != "https://api.eu.mailgun.net/v3" {
		t.Errorf("mailgun base URL = %q", got)
	}

	// An empty override must leave the vendor default in place rather than
	// blanking the host.
	def, err := buildSender(providerWith(t, panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID,
		&panmailv1.SendGridConfig{ApiKey: "k"}))
	if err != nil {
		t.Fatalf("sendgrid default: %v", err)
	}
	if got := def.(*sendgrid.Sender).BaseURL; got == "" {
		t.Error("an empty override blanked the default base URL")
	}
}

// Postmark rejects bulk mail sent on a transactional stream, so a campaign needs
// the stream set. It has to survive from config to sender.
func TestPostmarkMessageStreamIsCarriedThrough(t *testing.T) {
	s, err := buildSender(providerWith(t, panmailv1.ProviderType_PROVIDER_TYPE_POSTMARK,
		&panmailv1.PostmarkConfig{ServerToken: "t", MessageStream: "broadcast"}))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := s.(*postmark.Sender).MessageStream; got != "broadcast" {
		t.Errorf("message stream = %q, want broadcast", got)
	}
}

// Every API provider stores a secret, and the same round-trip rule applies as
// for SMTP: an edit that does not resend it must not wipe it.
func TestApiCredentialsSurviveAnEditThatOmitsThem(t *testing.T) {
	cases := []struct {
		name   string
		typ    panmailv1.ProviderType
		stored proto.Message
		blank  proto.Message
		check  func(t *testing.T, raw []byte)
	}{
		{
			"sendgrid", panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID,
			&panmailv1.SendGridConfig{ApiKey: "SG.secret"},
			&panmailv1.SendGridConfig{ApiKey: ""},
			func(t *testing.T, raw []byte) {
				c := &panmailv1.SendGridConfig{}
				_ = protojson.Unmarshal(raw, c)
				if c.ApiKey != "SG.secret" {
					t.Errorf("api key lost: %q", c.ApiKey)
				}
			},
		},
		{
			"ses", panmailv1.ProviderType_PROVIDER_TYPE_SES,
			&panmailv1.SesConfig{Region: "us-east-1", AccessKey: "AKIA", SecretKey: "shh"},
			&panmailv1.SesConfig{Region: "eu-west-1", AccessKey: "AKIA", SecretKey: ""},
			func(t *testing.T, raw []byte) {
				c := &panmailv1.SesConfig{}
				_ = protojson.Unmarshal(raw, c)
				if c.SecretKey != "shh" {
					t.Errorf("secret key lost: %q", c.SecretKey)
				}
				if c.Region != "eu-west-1" {
					t.Errorf("the edited region should have been kept: %q", c.Region)
				}
			},
		},
		{
			"postmark", panmailv1.ProviderType_PROVIDER_TYPE_POSTMARK,
			&panmailv1.PostmarkConfig{ServerToken: "tok"},
			&panmailv1.PostmarkConfig{ServerToken: "", MessageStream: "broadcast"},
			func(t *testing.T, raw []byte) {
				c := &panmailv1.PostmarkConfig{}
				_ = protojson.Unmarshal(raw, c)
				if c.ServerToken != "tok" {
					t.Errorf("server token lost: %q", c.ServerToken)
				}
				if c.MessageStream != "broadcast" {
					t.Errorf("the edited stream should have been kept: %q", c.MessageStream)
				}
			},
		},
		{
			"mailgun", panmailv1.ProviderType_PROVIDER_TYPE_MAILGUN,
			&panmailv1.MailgunConfig{Domain: "mg.example.com", ApiKey: "key"},
			&panmailv1.MailgunConfig{Domain: "mg.example.com", ApiKey: ""},
			func(t *testing.T, raw []byte) {
				c := &panmailv1.MailgunConfig{}
				_ = protojson.Unmarshal(raw, c)
				if c.ApiKey != "key" {
					t.Errorf("api key lost: %q", c.ApiKey)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored, err := protojson.Marshal(tc.stored)
			if err != nil {
				t.Fatal(err)
			}
			incoming, err := protojson.Marshal(tc.blank)
			if err != nil {
				t.Fatal(err)
			}

			merged, err := preserveStoredPassword(tc.typ, stored, incoming)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			tc.check(t, merged)
		})
	}
}

// The oneof switches used to be written out three times, so a provider added to
// two of them marshalled a nil config in the third. One helper each now, and
// every type must resolve through both.
func TestEveryProviderTypeResolvesInBothRequestShapes(t *testing.T) {
	creates := []*panmailv1.CreateEmailProviderRequest{
		{Config: &panmailv1.CreateEmailProviderRequest_Smtp{Smtp: &panmailv1.SmtpConfig{}}},
		{Config: &panmailv1.CreateEmailProviderRequest_Imap{Imap: &panmailv1.ImapConfig{}}},
		{Config: &panmailv1.CreateEmailProviderRequest_Pop3{Pop3: &panmailv1.Pop3Config{}}},
		{Config: &panmailv1.CreateEmailProviderRequest_Sendgrid{Sendgrid: &panmailv1.SendGridConfig{}}},
		{Config: &panmailv1.CreateEmailProviderRequest_Ses{Ses: &panmailv1.SesConfig{}}},
		{Config: &panmailv1.CreateEmailProviderRequest_Postmark{Postmark: &panmailv1.PostmarkConfig{}}},
		{Config: &panmailv1.CreateEmailProviderRequest_Mailgun{Mailgun: &panmailv1.MailgunConfig{}}},
	}
	for i, req := range creates {
		if createConfigMessage(req) == nil {
			t.Errorf("create config %d did not resolve", i)
		}
	}

	updates := []*panmailv1.UpdateEmailProviderRequest{
		{Config: &panmailv1.UpdateEmailProviderRequest_Smtp{Smtp: &panmailv1.SmtpConfig{}}},
		{Config: &panmailv1.UpdateEmailProviderRequest_Imap{Imap: &panmailv1.ImapConfig{}}},
		{Config: &panmailv1.UpdateEmailProviderRequest_Pop3{Pop3: &panmailv1.Pop3Config{}}},
		{Config: &panmailv1.UpdateEmailProviderRequest_Sendgrid{Sendgrid: &panmailv1.SendGridConfig{}}},
		{Config: &panmailv1.UpdateEmailProviderRequest_Ses{Ses: &panmailv1.SesConfig{}}},
		{Config: &panmailv1.UpdateEmailProviderRequest_Postmark{Postmark: &panmailv1.PostmarkConfig{}}},
		{Config: &panmailv1.UpdateEmailProviderRequest_Mailgun{Mailgun: &panmailv1.MailgunConfig{}}},
	}
	for i, req := range updates {
		if updateConfigMessage(req) == nil {
			t.Errorf("update config %d did not resolve", i)
		}
	}
}

// A create with no config at all used to panic on a type assertion of nil.
func TestACreateWithNoConfigIsRefusedNotPanicked(t *testing.T) {
	_, err := marshalCreateConfig(&panmailv1.CreateEmailProviderRequest{Name: "x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "configuration is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Every sender handed out is wrapped in an interceptor chain for panic recovery
// and telemetry. Two properties of that wrapping are easy to break silently.

// Ping is part of the gsmail.Sender interface, so it is promoted through the
// interceptor chain and the "Test connection" button keeps working. If a future
// gsmail moved Ping off the interface, this fails rather than the button
// starting to report every provider as unhealthy.
func TestAWrappedSenderCanStillBePinged(t *testing.T) {
	f := NewProviderFactory()
	t.Cleanup(func() { _ = f.Close() })

	s, err := f.CreateSender(providerWith(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		&panmailv1.SmtpConfig{Host: "smtp.example.com", Port: 587}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := s.(gsmail.Pinger); !ok {
		t.Error("the wrapped sender cannot be pinged; health checks would report every provider unhealthy")
	}
}

// Close is NOT on the gsmail.Sender interface, so an interceptor chain does not
// have one. Caching the wrapper instead of the raw sender would therefore leak
// every pooled SMTP connection at shutdown, silently and only in production.
func TestTheCacheHoldsSomethingClosable(t *testing.T) {
	f := NewProviderFactory()

	if _, err := f.CreateSender(providerWith(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		&panmailv1.SmtpConfig{Host: "smtp.example.com", Port: 587})); err != nil {
		t.Fatalf("create: %v", err)
	}

	pf := f.(*providerFactory)
	pf.mu.Lock()
	defer pf.mu.Unlock()
	if len(pf.senders) != 1 {
		t.Fatalf("expected one cached sender, got %d", len(pf.senders))
	}
	for _, cached := range pf.senders {
		if _, ok := cached.(interface{ Close() error }); !ok {
			t.Error("the cached sender has no Close; pooled connections would leak at shutdown")
		}
	}
}

// The same provider must keep returning a usable sender rather than a new one
// each time, or the connection pool is pointless.
func TestRepeatedCreatesReuseTheCachedSender(t *testing.T) {
	f := NewProviderFactory()
	t.Cleanup(func() { _ = f.Close() })

	p := providerWith(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		&panmailv1.SmtpConfig{Host: "smtp.example.com", Port: 587})

	for range 3 {
		if _, err := f.CreateSender(p); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	pf := f.(*providerFactory)
	pf.mu.Lock()
	defer pf.mu.Unlock()
	if len(pf.senders) != 1 {
		t.Errorf("expected the sender to be reused, got %d cached", len(pf.senders))
	}
}
