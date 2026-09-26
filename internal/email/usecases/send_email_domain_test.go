package usecases

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gsoultan/panmail/internal/ratelimit"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// smtpProvider builds an SMTP provider whose host is deliberately unrelated to
// any sender's domain, which is the normal shape for a hosted ESP.
func smtpProvider(t *testing.T, host string, allowedDomains []string) *providerEntities.EmailProvider {
	t.Helper()
	cfg, err := json.Marshal(map[string]any{"host": host, "port": 587})
	if err != nil {
		t.Fatalf("marshal smtp config: %v", err)
	}
	return &providerEntities.EmailProvider{
		ID:             testProviderID,
		Name:           "ESP",
		Type:           panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		Config:         cfg,
		AllowedDomains: allowedDomains,
	}
}

func newDomainTestUsecase(provider *providerEntities.EmailProvider) (SendEmailUsecase, *mockSender) {
	sender := &mockSender{}
	return NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: provider},
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: sender},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
	}), sender
}

// A From address whose domain differs from the SMTP hostname is the normal case
// for every hosted ESP: SendGrid, SES, Mailgun and Postmark all relay mail for
// domains that have nothing to do with their own hostname.
//
// This used to be rejected outright by a check comparing the From domain with
// the provider host domain, which made Panmail unusable with any of them.
func TestSenderDomainNeedNotMatchTheProviderHost(t *testing.T) {
	hosts := []struct {
		name string
		host string
		from string
	}{
		{"sendgrid", "smtp.sendgrid.net", "noreply@yourcompany.com"},
		{"ses", "email-smtp.us-east-1.amazonaws.com", "billing@yourcompany.com"},
		{"mailgun", "smtp.mailgun.org", "support@yourcompany.co.uk"},
		{"postmark", "smtp.postmarkapp.com", "hello@yourcompany.io"},
	}

	for _, tc := range hosts {
		t.Run(tc.name, func(t *testing.T) {
			// No AllowedDomains: the operator has not restricted this provider.
			u, sender := newDomainTestUsecase(smtpProvider(t, tc.host, nil))

			req := &panmailv1.SendEmailRequest{
				ProviderId: testProviderID,
				From:       tc.from,
				To:         []string{"recipient@example.com"},
				Subject:    "Hello",
				BodyHtml:   "<html><body>hi</body></html>",
			}

			ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
			if _, err := u.SendEmail(ctx, testTenantID, req); err != nil {
				t.Fatalf("send through %s should succeed, got: %v", tc.host, err)
			}
			if len(sender.sentEmails) != 1 {
				t.Fatalf("expected 1 sent email, got %d", len(sender.sentEmails))
			}
		})
	}
}

// Removing the host check must not weaken anti-spoofing. AllowedDomains is the
// control that decides which senders a provider may relay for, and it is
// enforced against the From address regardless of the provider's hostname.
func TestAllowedDomainsStillBlocksASpoofedSender(t *testing.T) {
	provider := smtpProvider(t, "smtp.sendgrid.net", []string{"yourcompany.com"})
	u, sender := newDomainTestUsecase(provider)

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "ceo@someone-elses-bank.com",
		To:         []string{"victim@example.com"},
		Subject:    "Urgent wire transfer",
		BodyHtml:   "<html><body>please pay</body></html>",
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	_, err := u.SendEmail(ctx, testTenantID, req)
	if err == nil {
		t.Fatal("a From domain outside AllowedDomains must be rejected")
	}
	if !strings.Contains(err.Error(), "not authorized to send for domain") {
		t.Errorf("expected an authorization error, got: %v", err)
	}
	if len(sender.sentEmails) != 0 {
		t.Errorf("nothing should have been sent, got %d", len(sender.sentEmails))
	}
}

// The permitted half of the same control: a sender inside AllowedDomains goes
// through even though it shares nothing with the provider's hostname.
func TestAllowedDomainsPermitsAnAuthorizedSender(t *testing.T) {
	provider := smtpProvider(t, "smtp.sendgrid.net", []string{"yourcompany.com"})
	u, sender := newDomainTestUsecase(provider)

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "noreply@yourcompany.com",
		To:         []string{"recipient@example.com"},
		Subject:    "Hello",
		BodyHtml:   "<html><body>hi</body></html>",
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, req); err != nil {
		t.Fatalf("an authorized sender must go through, got: %v", err)
	}
	if len(sender.sentEmails) != 1 {
		t.Fatalf("expected 1 sent email, got %d", len(sender.sentEmails))
	}
}

// senderDomain is the one extractor both halves of the send path use, so its
// answers decide whether admission and delivery agree.
func TestSenderDomain(t *testing.T) {
	tests := []struct {
		from    string
		want    string
		wantErr bool
	}{
		{from: "alice@example.com", want: "example.com"},
		{from: "alice@Example.COM", want: "example.com"},
		// The display-name form admission already accepts. Splitting the raw
		// string on "@" read this as `example.com>`.
		{from: "Alice <alice@example.com>", want: "example.com"},
		{from: `"Smith, Alice" <alice@example.com>`, want: "example.com"},
		{from: "  alice@example.com  ", want: "example.com"},
		// A quoted local part may hold an "@" of its own; the domain is after
		// the last one.
		{from: `"a@b"@example.com`, want: "example.com"},
		{from: "no-at-sign", wantErr: true},
		{from: "alice@", wantErr: true},
		{from: "@example.com", wantErr: true},
		{from: "", wantErr: true},
	}

	for _, tt := range tests {
		got, err := senderDomain(tt.from)
		if (err != nil) != tt.wantErr {
			t.Errorf("senderDomain(%q) error = %v, wantErr %v", tt.from, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("senderDomain(%q) = %q, want %q", tt.from, got, tt.want)
		}
	}
}

func TestProviderAllowsDomain(t *testing.T) {
	restricted := &providerEntities.EmailProvider{AllowedDomains: []string{"yourcompany.com", " Other.org "}}
	open := &providerEntities.EmailProvider{}

	tests := []struct {
		name   string
		p      *providerEntities.EmailProvider
		domain string
		want   bool
	}{
		{"an unrestricted provider allows anything", open, "anywhere.net", true},
		{"a listed domain", restricted, "yourcompany.com", true},
		{"case does not matter", restricted, "other.org", true},
		{"stray whitespace in the list does not matter", restricted, "other.org", true},
		{"an unlisted domain", restricted, "someone-elses-bank.com", false},
		{"a suffix is not a match", restricted, "evil-yourcompany.com", false},
	}
	for _, tt := range tests {
		if got := providerAllowsDomain(tt.p, tt.domain); got != tt.want {
			t.Errorf("%s: providerAllowsDomain(%q) = %v, want %v", tt.name, tt.domain, got, tt.want)
		}
	}
}

// The latent bug. Admission accepts the display-name form, but delivery split
// the raw From on "@" and read the domain as `yourcompany.com>` -- so a
// provider with AllowedDomains refused every such send, after admission had
// already answered 200.
func TestADisplayNameSenderIsDeliveredThroughARestrictedProvider(t *testing.T) {
	provider := smtpProvider(t, "smtp.sendgrid.net", []string{"yourcompany.com"})
	u, sender := newDomainTestUsecase(provider)

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "Alice <alice@yourcompany.com>",
		To:         []string{"customer@example.com"},
		Subject:    "Your receipt",
		BodyHtml:   "<html><body>thanks</body></html>",
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, req); err != nil {
		t.Fatalf("a display-name sender at an authorized domain was refused: %v", err)
	}
	if len(sender.sentEmails) != 1 {
		t.Fatalf("sent %d, want 1", len(sender.sentEmails))
	}
}

// Refused while the caller is still waiting, rather than answered 200 and
// failed later in the worker where nobody is listening.
func TestAdmissionRefusesADomainTheProviderIsNotAuthorizedFor(t *testing.T) {
	provider := smtpProvider(t, "smtp.sendgrid.net", []string{"yourcompany.com"})
	u, sender := newDomainTestUsecase(provider)

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "ceo@someone-elses-bank.com",
		To:         []string{"victim@example.com"},
		Subject:    "Urgent wire transfer",
		BodyHtml:   "<html><body>please pay</body></html>",
	}

	// No SkipOutboxKey: this is admission, not delivery.
	_, err := u.SendEmail(context.Background(), testTenantID, req)

	var refused *ProviderDomainRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want a ProviderDomainRefusedError at admission", err)
	}
	if refused.Domain != "someone-elses-bank.com" || refused.Provider != "ESP" {
		t.Errorf("refusal = %+v", refused)
	}
	if len(sender.sentEmails) != 0 {
		t.Errorf("sent %d at admission, want 0", len(sender.sentEmails))
	}
}

// Admission must not refuse what delivery would accept. The display-name form
// passing here is the other half of the latent bug above.
//
// A full client-mode harness, so admission runs to the end and the assertion
// can be that it succeeded -- not merely that it did not fail on the domain,
// which would pass just as well for a send that broke a line later.
func TestAdmissionAcceptsADisplayNameSenderAtAnAuthorizedDomain(t *testing.T) {
	provider := smtpProvider(t, "smtp.sendgrid.net", []string{"yourcompany.com"})
	outbox := &mockOutboxRepo{}
	u := NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: provider},
		TemplateRepo:    &mockTemplateRepo{},
		SuppressionRepo: &mockSuppressionRepo{},
		OutboxRepo:      outbox,
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: &mockSender{}},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
	})

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "Alice <alice@yourcompany.com>",
		To:         []string{"customer@example.com"},
		Subject:    "Your receipt",
		BodyHtml:   "<html><body>thanks</body></html>",
	}

	res, err := u.SendEmail(context.Background(), testTenantID, req)
	if err != nil {
		t.Fatalf("admission refused an authorized display-name sender: %v", err)
	}
	if res == nil || res.MessageId == "" {
		t.Fatalf("response = %+v, want a queued message id", res)
	}
}

// A send that can never go out must not spend anyone's allowance, or retrying
// a misconfigured From drains the tenant's ceiling on mail that was refused.
func TestADomainRefusalDoesNotSpendTheRate(t *testing.T) {
	provider := smtpProvider(t, "smtp.sendgrid.net", []string{"yourcompany.com"})
	limiter := ratelimit.New()
	u := NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: provider},
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: &mockSender{}},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
		Limiter:         limiter,
		SendLimits:      &stubLimits{limit: ratelimit.Limit{PerMinute: 60, Burst: 60}},
	})

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "ceo@someone-elses-bank.com",
		To:         []string{"victim@example.com"},
		Subject:    "x",
		BodyHtml:   "<html><body>x</body></html>",
	}
	for range 5 {
		if _, err := u.SendEmail(context.Background(), testTenantID, req); err == nil {
			t.Fatal("a refused domain was admitted")
		}
	}

	if limiter.Tracked() != 0 {
		t.Fatalf("the limiter holds %d buckets; a refused send charged the rate", limiter.Tracked())
	}
}

// The message the refusal carried before it was typed, byte for byte: the
// outbox worker records it, and it is what an operator greps a log for.
func TestProviderDomainRefusalMessageIsUnchanged(t *testing.T) {
	got := (&ProviderDomainRefusedError{Provider: "ESP", Domain: "example.com"}).Error()
	want := "provider ESP is not authorized to send for domain example.com"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
