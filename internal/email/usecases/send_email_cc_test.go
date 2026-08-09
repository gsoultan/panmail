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
	return newUsecaseWithBaseURL("http://localhost")
}

func newUsecaseWithBaseURL(baseURL string) (SendEmailUsecase, *mockSender) {
	sender := &mockSender{}
	provider := &providerEntities.EmailProvider{
		ID: testProviderID, Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
	}
	return NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: provider},
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: sender},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         baseURL,
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

// Gmail and Yahoo have required the RFC 8058 header *pair* from bulk senders
// since February 2024, and they measure compliance across a sending domain — so
// one campaign that omits it degrades delivery for every other message from that
// domain. List-Unsubscribe on its own does not satisfy the requirement.
func TestEveryMessageCarriesOneClickUnsubscribe(t *testing.T) {
	// An https base URL, because RFC 8058 one-click works by the mailbox
	// provider POSTing to the target: gsmail refuses to set the pair without
	// one, and it is right to.
	u, sender := newUsecaseWithBaseURL("https://mail.example.com")

	req := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"to1@example.com"},
		Cc:         []string{"cc1@example.com"},
		Subject:    "Hello",
		BodyHtml:   "<html><body>hi</body></html>",
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, req); err != nil {
		t.Fatalf("send: %v", err)
	}

	for _, msg := range sender.sentEmails {
		if !msg.HasOneClickUnsubscribe() {
			t.Errorf("copy for %v is missing the RFC 8058 header pair", msg.Envelope)
		}
		// The link has to be per recipient, or unsubscribing one address would
		// have to guess which mailbox actually asked.
		encoded := base64.RawURLEncoding.EncodeToString([]byte(msg.Envelope[0]))
		if !strings.Contains(msg.Headers["List-Unsubscribe"], encoded) {
			t.Errorf("the unsubscribe link for %v does not identify that recipient", msg.Envelope)
		}
		// Signed, or the endpoint becomes a way to suppress arbitrary addresses.
		if !strings.Contains(msg.Headers["List-Unsubscribe"], "sig=") {
			t.Errorf("the unsubscribe link for %v is unsigned", msg.Envelope)
		}
	}
}

// A plain-http deployment cannot have one-click unsubscribe: the provider has to
// POST to the target, so gsmail refuses a non-https one. The send must still go
// out — refusing would turn an http base URL into a total outage — but the
// message then does not satisfy the Gmail and Yahoo requirement, which is a
// deployment problem worth knowing about rather than a silent one.
func TestAPlainHTTPBaseURLCannotCarryOneClickUnsubscribe(t *testing.T) {
	u, sender := newUsecaseWithBaseURL("http://localhost:8080")

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"to@example.com"},
		Subject:    "Hello",
		BodyHtml:   "<html><body>hi</body></html>",
	}); err != nil {
		t.Fatalf("the message must still be sent: %v", err)
	}

	if len(sender.sentEmails) != 1 {
		t.Fatalf("expected the message to be sent, got %d copies", len(sender.sentEmails))
	}
	if sender.sentEmails[0].HasOneClickUnsubscribe() {
		t.Error("an http target cannot satisfy RFC 8058 and must not be advertised as if it did")
	}
}

// A malformed address that reaches a provider becomes a rejected RCPT TO, and
// enough of those get a sending domain throttled. Catching them here keeps them
// out of the outbox entirely.
func TestMalformedAddressesAreRefusedBeforeQueueing(t *testing.T) {
	cases := []struct {
		name string
		req  *panmailv1.SendEmailRequest
	}{
		{"no at sign in recipient", &panmailv1.SendEmailRequest{
			From: "from@example.com", To: []string{"not-an-address"}}},
		{"empty recipient", &panmailv1.SendEmailRequest{
			From: "from@example.com", To: []string{""}}},
		{"malformed cc", &panmailv1.SendEmailRequest{
			From: "from@example.com", To: []string{"ok@example.com"}, Cc: []string{"bad@"}}},
		{"malformed bcc", &panmailv1.SendEmailRequest{
			From: "from@example.com", To: []string{"ok@example.com"}, Bcc: []string{"@example.com"}}},
		{"malformed from fails the whole message", &panmailv1.SendEmailRequest{
			From: "not-an-address", To: []string{"ok@example.com"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, sender := newUsecaseWithBaseURL("https://mail.example.com")
			tc.req.ProviderId = testProviderID
			tc.req.Subject = "Hello"
			tc.req.BodyHtml = "<html><body>hi</body></html>"

			ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
			if _, err := u.SendEmail(ctx, testTenantID, tc.req); err == nil {
				t.Error("expected the send to be refused")
			}
			if len(sender.sentEmails) != 0 {
				t.Errorf("nothing should have been sent, got %d", len(sender.sentEmails))
			}
		})
	}
}

// Validation must not reject addresses that are unusual but legal, or it
// becomes the thing blocking legitimate mail.
func TestLegitimateAddressesAreAccepted(t *testing.T) {
	for _, addr := range []string{
		"plain@example.com",
		"with+tag@example.com",
		"dotted.name@example.co.uk",
		"under_score@example.com",
		"Display Name <named@example.com>",
	} {
		t.Run(addr, func(t *testing.T) {
			u, sender := newUsecaseWithBaseURL("https://mail.example.com")
			ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
			_, err := u.SendEmail(ctx, testTenantID, &panmailv1.SendEmailRequest{
				ProviderId: testProviderID,
				From:       "from@example.com",
				To:         []string{addr},
				Subject:    "Hello",
				BodyHtml:   "<html><body>hi</body></html>",
			})
			if err != nil {
				t.Errorf("%q is a legal address but was refused: %v", addr, err)
			}
			if len(sender.sentEmails) != 1 {
				t.Errorf("expected the message to be sent, got %d copies", len(sender.sentEmails))
			}
		})
	}
}
