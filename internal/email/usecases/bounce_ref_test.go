package usecases

import (
	"context"
	"testing"

	gsmail "github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// Inbound bounce detection believes a delivery report only when the headers it
// returns carry the reference the send path signed onto that copy. So every
// copy has to carry one, it has to survive rendering onto the wire, and it has
// to verify for the address that copy went to and for no other.
func TestEveryCopyCarriesABounceReferenceForItsOwnRecipient(t *testing.T) {
	provider := &providerEntities.EmailProvider{
		ID: testProviderID, Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
	}
	signer := tracking.NewSigner([]byte("test-tracking-key"))
	sender := &mockSender{}
	u := NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: provider},
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: sender},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  signer,
	})

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	resp, err := u.SendEmail(ctx, testTenantID, &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		// The display-name form: a report names the bare address.
		To:       []string{"A Person <a@example.com>", "b@example.com"},
		Subject:  "Hello",
		BodyText: "hi",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(sender.sentEmails) != 2 {
		t.Fatalf("sent %d copies, want 2", len(sender.sentEmails))
	}

	for _, copy := range sender.sentEmails {
		own, other := "a@example.com", "b@example.com"
		if envelopeOf(copy) == "b@example.com" {
			own, other = other, own
		}

		// What a remote server would quote back: the header as rendered.
		raw, err := gsmail.RenderMessage(copy)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		parsed, err := gsmail.ParseRawEmail(raw)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		ref := parsed.Header(tracking.BounceRefHeader)
		if ref == "" {
			t.Fatalf("the copy for %s carries no %s", own, tracking.BounceRefHeader)
		}

		id, err := signer.VerifyBounceRef(ref, testTenantID, own)
		if err != nil || id != resp.MessageId {
			t.Errorf("the reference on %s's copy verified as (%q, %v), want message %q", own, id, err, resp.MessageId)
		}
		if _, err := signer.VerifyBounceRef(ref, testTenantID, other); err == nil {
			t.Errorf("the reference on %s's copy also verifies for %s", own, other)
		}
	}
}
