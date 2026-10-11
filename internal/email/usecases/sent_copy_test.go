package usecases

import (
	"context"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
)

type recordingSentCopies struct {
	queued []*panmailv1.SendEmailRequest
	ids    []string
}

func (r *recordingSentCopies) Enqueue(_ context.Context, _, messageID string, req *panmailv1.SendEmailRequest) error {
	r.queued = append(r.queued, req)
	r.ids = append(r.ids, messageID)
	return nil
}

func sentCopyUsecase(copies SentCopyQueue) SendEmailUsecase {
	return NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo: &mockProviderRepo{provider: &providerEntities.EmailProvider{
			ID: testProviderID, TenantID: testTenantID, Name: "test", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		}},
		TemplateRepo: &mockTemplateRepo{}, SuppressionRepo: &mockSuppressionRepo{},
		OutboxRepo: &mockOutboxRepo{}, EventUsecase: &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: &mockSender{}}, Renderer: NewTemplateRenderer(),
		SentCopies: copies,
	})
}

func TestAnAcceptedMessageQueuesItsSentCopyOnlyWhenAsked(t *testing.T) {
	copies := &recordingSentCopies{}
	u := sentCopyUsecase(copies)

	base := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID, From: "from@example.com", To: []string{"a@example.com"},
		Subject: "Hi {{name}}", BodyText: "Hello {{name}}",
		TemplateData: nil, IdempotencyKey: "k1",
	}
	if _, err := u.SendEmail(context.Background(), testTenantID, base); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	if len(copies.queued) != 0 {
		t.Fatalf("queued a copy nobody asked for")
	}

	asked := &panmailv1.SendEmailRequest{
		ProviderId: testProviderID, From: "from@example.com", To: []string{"a@example.com"},
		Subject: "Hi", BodyText: "Hello", SaveSentCopy: true, IdempotencyKey: "k2",
	}
	res, err := u.SendEmail(context.Background(), testTenantID, asked)
	if err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	if len(copies.queued) != 1 || copies.ids[0] != res.MessageId {
		t.Fatalf("queued %d copies (ids %v), want one for %s", len(copies.queued), copies.ids, res.MessageId)
	}
	got := copies.queued[0]
	if got.Subject != "Hi" || got.BodyText != "Hello" || got.IdempotencyKey != "" {
		t.Errorf("queued copy = %+v", got)
	}
	// The queued copy is its own message: changing it does not reach the
	// request being delivered.
	if asked.IdempotencyKey != "k2" {
		t.Errorf("queueing the copy changed the caller's request")
	}
}

func TestSentCopyEmailCarriesTheVisibleAudienceAndNoTracking(t *testing.T) {
	msg := SentCopyEmail(&panmailv1.SendEmailRequest{
		From: "a@example.com", To: []string{"b@example.com"}, Cc: []string{"c@example.com"},
		Bcc: []string{"d@example.com"}, Subject: "S", BodyHtml: "<p>x</p>", ReplyTo: "r@example.com",
		InReplyTo: "<o@example.com>",
	})
	if len(msg.Bcc) != 0 || len(msg.Envelope) != 0 {
		t.Errorf("the copy names blind recipients: Bcc=%v Envelope=%v", msg.Bcc, msg.Envelope)
	}
	if msg.ReplyTo != "r@example.com" || msg.Headers["In-Reply-To"] != "<o@example.com>" {
		t.Errorf("reply headers lost: %+v", msg)
	}
	if string(msg.HTMLBody) != "<p>x</p>" {
		t.Errorf("HTML = %q, want it untouched by tracking", msg.HTMLBody)
	}
}
