package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"google.golang.org/protobuf/encoding/protojson"
)

func runPermanentRefusal(t *testing.T, refusal error) (*workerMockOutboxRepo, *mockEmailUsecase, *mockSuppressionUsecase) {
	t.Helper()
	outbox := &workerMockOutboxRepo{}
	send := &mockEmailUsecase{err: refusal}
	sup := &mockSuppressionUsecase{}
	w := NewQueueWorker(outbox, send, sup, &mockTenantUsecase{}, time.Second).(*queueWorker)

	req := panmailv1.SendEmailRequest{
		To: []string{"a@example.com", "b@example.com"}, From: "noreply@example.com", Subject: "x", Body: "x",
	}
	raw, _ := protojson.Marshal(&req)
	outbox.emails = []*entities.OutboxEmail{{
		ID: "msg-1", TenantID: "tenant1", Request: raw,
		Status: entities.OutboxStatusPending, NextRetryAt: time.Now(),
	}}
	w.processPending(context.Background())
	return outbox, send, sup
}

// A template deleted while messages sat in the outbox sent each of them round
// the whole retry schedule -- eight attempts over two days -- failing
// identically every time.
func TestAMissingTemplateFailsOnTheFirstAttempt(t *testing.T) {
	outbox, _, _ := runPermanentRefusal(t, &TemplateRefusedError{TemplateID: "gone"})

	if outbox.lastUpdate == nil || outbox.lastUpdate.Status != entities.OutboxStatusFailed {
		t.Fatalf("row = %+v, want failed on the first attempt", outbox.lastUpdate)
	}
	if outbox.lastUpdate.RetryCount != 1 {
		t.Errorf("retry count = %d, want 1 attempt", outbox.lastUpdate.RetryCount)
	}
}

// Rendering against this message's data fails the same way every time too.
func TestATemplateThatWillNotRenderFailsOnTheFirstAttempt(t *testing.T) {
	outbox, _, _ := runPermanentRefusal(t, &TemplateRefusedError{
		TemplateID: "t1", Stage: "subject", Err: errors.New("unexpected }"),
	})
	if outbox.lastUpdate == nil || outbox.lastUpdate.Status != entities.OutboxStatusFailed {
		t.Fatalf("row = %+v, want failed on the first attempt", outbox.lastUpdate)
	}
}

// Left to the classifier, a missing template read as BOUNCED: it counted
// towards every recipient's bounce rate, and the tenant's webhook heard
// MAIL_BOUNCED for mail that was never offered to any server.
func TestARefusalIsRecordedAsRejectedNotBounced(t *testing.T) {
	_, send, _ := runPermanentRefusal(t, &TemplateRefusedError{TemplateID: "gone"})

	if len(send.recorded) == 0 {
		t.Fatal("no outcome was recorded")
	}
	for _, ty := range send.recorded {
		if ty != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED {
			t.Fatalf("recorded %v, want REJECTED for every recipient", send.recorded)
		}
	}
}

// A refusal says nothing about the recipient, so it must not suppress anyone.
func TestARefusalSuppressesNoOne(t *testing.T) {
	_, _, sup := runPermanentRefusal(t, &TemplateRefusedError{TemplateID: "gone"})
	if len(sup.suppressedEmails) != 0 {
		t.Fatalf("suppressed %v for a missing template", sup.suppressedEmails)
	}
}

// The domain refusal already classified as REJECTED; recording refusals as
// REJECTED explicitly must leave it exactly where it was.
func TestTheDomainRefusalIsUnchanged(t *testing.T) {
	outbox, send, _ := runPermanentRefusal(t, &ProviderDomainRefusedError{Provider: "ESP", Domain: "example.com"})

	if outbox.lastUpdate == nil || outbox.lastUpdate.Status != entities.OutboxStatusFailed {
		t.Fatalf("row = %+v, want failed on the first attempt", outbox.lastUpdate)
	}
	for _, ty := range send.recorded {
		if ty != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED {
			t.Fatalf("recorded %v, want REJECTED", send.recorded)
		}
	}
}
