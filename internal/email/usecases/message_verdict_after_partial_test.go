package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/pkg/tracking"
	"google.golang.org/protobuf/encoding/protojson"
)

// verdictHarness keeps the provider reachable between passes, so a test can
// change its configuration while a partly delivered message waits.
type verdictHarness struct {
	worker   *queueWorker
	outbox   *workerMockOutboxRepo
	events   *mockEventUsecase
	provider *providerEntities.EmailProvider
	failBusy bool
}

func newVerdictHarness(to ...string) *verdictHarness {
	h := &verdictHarness{outbox: &workerMockOutboxRepo{}, events: &mockEventUsecase{}, failBusy: true}
	h.provider = &providerEntities.EmailProvider{
		ID: testProviderID, Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
	}
	sender := &mockSenderFunc{sendFn: func(e gsmail.Email) error {
		if envelopeOf(e) == "busy@example.com" && h.failBusy {
			return errors.New("421 4.7.0 try again later")
		}
		return nil
	}}
	send := NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo: &mockProviderRepo{provider: h.provider}, EventUsecase: h.events,
		ProviderFactory: &mockFactory{sender: sender}, Renderer: NewTemplateRenderer(),
		BaseURL: "http://localhost", TrackingSigner: tracking.NewSigner([]byte("test-tracking-key")),
	})
	h.worker = NewQueueWorker(h.outbox, send, &mockSuppressionUsecase{}, &mockTenantUsecase{}, time.Second).(*queueWorker)

	req := panmailv1.SendEmailRequest{ProviderId: testProviderID, From: "noreply@example.com", To: to, Subject: "x", Body: "x"}
	raw, _ := protojson.Marshal(&req)
	h.outbox.emails = []*entities.OutboxEmail{{
		ID: "msg-1", TenantID: testTenantID, Request: raw,
		Status: entities.OutboxStatusPending, NextRetryAt: time.Now(),
	}}
	return h
}

func (h *verdictHarness) pass() {
	h.outbox.lastUpdate = nil
	h.worker.processPending(context.Background())
	if row := h.outbox.lastUpdate; row != nil {
		row.Status, row.NextRetryAt = entities.OutboxStatusPending, time.Now()
		h.outbox.emails = []*entities.OutboxEmail{row}
	}
}

func (h *verdictHarness) typesFor(recipient string) []panmailv1.EmailEventType {
	var out []panmailv1.EmailEventType
	for _, e := range h.events.events {
		if e.Recipient == recipient {
			out = append(out, e.Type)
		}
	}
	return out
}

// A partly delivered message's row now survives while a co-recipient is
// retried, so a message-level failure can arrive on a later pass. Recording
// that verdict against everyone on the request gave a delivered recipient a
// REJECTED after its DELIVERED -- and fired MAIL_REJECTED for mail that arrived.
func TestAMessageLevelVerdictSparesRecipientsAlreadyDelivered(t *testing.T) {
	h := newVerdictHarness("ok@example.com", "busy@example.com")

	h.pass() // ok@ delivered, busy@ deferred
	// While busy@ waits, an operator restricts the provider to another domain.
	h.provider.AllowedDomains = []string{"elsewhere.com"}
	h.pass()

	for _, ty := range h.typesFor("ok@example.com") {
		if ty == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED {
			t.Fatalf("ok@ events = %v: delivered, then rejected", h.typesFor("ok@example.com"))
		}
	}
	rejected := false
	for _, ty := range h.typesFor("busy@example.com") {
		if ty == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED {
			rejected = true
		}
	}
	if !rejected {
		t.Errorf("busy@ events = %v, want the rejection: it is the one still undelivered", h.typesFor("busy@example.com"))
	}
}

// On a first pass nobody is finished, so a message-level verdict still reaches
// every recipient, as it always has.
func TestAFirstPassVerdictStillReachesEveryone(t *testing.T) {
	h := newVerdictHarness("a@example.com", "b@example.com")
	h.provider.AllowedDomains = []string{"elsewhere.com"}

	h.pass()

	for _, r := range []string{"a@example.com", "b@example.com"} {
		got := h.typesFor(r)
		if len(got) != 1 || got[0] != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED {
			t.Errorf("%s events = %v, want one REJECTED", r, got)
		}
	}
}

// If the history cannot be read, a verdict is recorded for everyone rather than
// lost: a failure nobody hears about is worse than a duplicate event.
func TestUnreadableHistoryKeepsEveryRecipient(t *testing.T) {
	w := &queueWorker{emailUsecase: &historyFails{}}
	got := w.unfinishedRecipients(context.Background(), &entities.OutboxEmail{ID: "m", TenantID: "t"},
		[]string{"a@example.com", "b@example.com"})
	if len(got) != 2 {
		t.Fatalf("kept %v, want both when the history is unreadable", got)
	}
}

type historyFails struct{ mockEmailUsecase }

func (historyFails) FinishedRecipients(context.Context, string, string) (map[string]bool, error) {
	return nil, errors.New("database unreachable")
}
