package usecases

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// CC used to be delivered as BCC.
//
// Each recipient gets its own copy so the open pixel and the signed links are
// personal to that address, and populating Cc would previously have added every
// Cc address to the envelope on every iteration — one copy each, per recipient.
// So Cc was left empty, and a Cc recipient could not see who else was copied.
//
// gsmail.Email.Envelope separates the two: the headers name the whole visible
// audience while the envelope names the single address this copy is for.

func newCCTestUsecase() (SendEmailUsecase, *mockSender) {
	sender := &mockSender{}
	provider := &providerEntities.EmailProvider{
		ID: testProviderID, Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
	}
	return NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: provider},
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: sender},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
	}), sender
}

func TestCcIsVisibleAndDeliveryStaysPerRecipient(t *testing.T) {
	u, sender := newCCTestUsecase()

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"to1@example.com", "to2@example.com"},
		Cc:         []string{"cc1@example.com"},
		Subject:    "Hello",
		BodyHtml:   `<html><body><a href="https://example.com">link</a></body></html>`,
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, req); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Three addresses, three copies — one per recipient, as before.
	if len(sender.sentEmails) != 3 {
		t.Fatalf("expected one copy per recipient, got %d", len(sender.sentEmails))
	}

	for _, msg := range sender.sentEmails {
		// The envelope is what actually decides delivery, and it must name
		// exactly one address or the Cc list multiplies the send.
		if len(msg.Envelope) != 1 {
			t.Errorf("expected a single envelope recipient, got %v", msg.Envelope)
		}

		// The headers describe the whole visible audience.
		if len(msg.To) != 2 {
			t.Errorf("To header should name both To recipients, got %v", msg.To)
		}
		if len(msg.Cc) != 1 || msg.Cc[0] != "cc1@example.com" {
			t.Errorf("Cc header should name the cc recipient, got %v", msg.Cc)
		}
	}

	// Every address received exactly one copy.
	delivered := map[string]int{}
	for _, msg := range sender.sentEmails {
		for _, rcpt := range msg.Envelope {
			delivered[rcpt]++
		}
	}
	for _, addr := range []string{"to1@example.com", "to2@example.com", "cc1@example.com"} {
		if delivered[addr] != 1 {
			t.Errorf("%s received %d copies, want 1", addr, delivered[addr])
		}
	}
}

// The reason the per-recipient loop exists in the first place.
func TestEachCopyKeepsItsOwnTrackingIdentity(t *testing.T) {
	u, sender := newCCTestUsecase()

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"to1@example.com"},
		Cc:         []string{"cc1@example.com"},
		Subject:    "Hello",
		BodyHtml:   `<html><body><a href="https://example.com">link</a></body></html>`,
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, req); err != nil {
		t.Fatalf("send: %v", err)
	}

	for _, addr := range []string{"to1@example.com", "cc1@example.com"} {
		encoded := base64.RawURLEncoding.EncodeToString([]byte(addr))
		found := false
		for _, msg := range sender.sentEmails {
			if len(msg.Envelope) == 1 && msg.Envelope[0] == addr {
				if strings.Contains(string(msg.HTMLBody), encoded) {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("the copy delivered to %s does not carry its own tracking identity", addr)
		}
	}
}

// A blind address must never appear in a header, but must still get a copy.
func TestBccReceivesACopyWithoutBeingDisclosed(t *testing.T) {
	u, sender := newCCTestUsecase()

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"to1@example.com"},
		Bcc:        []string{"secret@example.com"},
		Subject:    "Hello",
		BodyHtml:   "<html><body>hi</body></html>",
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, req); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(sender.sentEmails) != 2 {
		t.Fatalf("expected a copy for the To and the Bcc recipient, got %d", len(sender.sentEmails))
	}

	sawBcc := false
	for _, msg := range sender.sentEmails {
		if len(msg.Bcc) != 0 {
			t.Errorf("a Bcc header was set, disclosing %v", msg.Bcc)
		}
		for _, addr := range msg.To {
			if addr == "secret@example.com" {
				t.Error("the blind address appeared in the To header")
			}
		}
		for _, addr := range msg.Cc {
			if addr == "secret@example.com" {
				t.Error("the blind address appeared in the Cc header")
			}
		}
		if len(msg.Envelope) == 1 && msg.Envelope[0] == "secret@example.com" {
			sawBcc = true
		}
	}
	if !sawBcc {
		t.Error("the blind recipient never received a copy")
	}
}

// With no Cc there is nothing to disclose, and the headers stay minimal.
func TestASingleRecipientSendIsUnchanged(t *testing.T) {
	u, sender := newCCTestUsecase()

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"only@example.com"},
		Subject:    "Hello",
		BodyHtml:   "<html><body>hi</body></html>",
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, req); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(sender.sentEmails) != 1 {
		t.Fatalf("expected 1 copy, got %d", len(sender.sentEmails))
	}
	msg := sender.sentEmails[0]
	if len(msg.Cc) != 0 {
		t.Errorf("no Cc was requested, got %v", msg.Cc)
	}
	if len(msg.Envelope) != 1 || msg.Envelope[0] != "only@example.com" {
		t.Errorf("unexpected envelope %v", msg.Envelope)
	}
}
