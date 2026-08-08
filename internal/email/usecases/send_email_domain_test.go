package usecases

import (
	"context"
	"encoding/json"
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
