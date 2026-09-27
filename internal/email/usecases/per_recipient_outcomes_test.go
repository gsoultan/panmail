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

// outcomeHarness drives the real send path through the real worker, because
// the bugs these tests pin live in the seam between them: the send path
// reporting a pass, and the worker deciding what that pass meant.
type outcomeHarness struct {
	t        *testing.T
	worker   *queueWorker
	outbox   *workerMockOutboxRepo
	events   *mockEventUsecase
	suppress *mockSuppressionUsecase
	// replies is what the provider answers per recipient, consumed in order;
	// an exhausted list means success.
	replies  map[string][]error
	attempts map[string]int
}

func newOutcomeHarness(t *testing.T, to ...string) *outcomeHarness {
	h := &outcomeHarness{
		t: t, outbox: &workerMockOutboxRepo{}, events: &mockEventUsecase{},
		suppress: &mockSuppressionUsecase{}, replies: map[string][]error{}, attempts: map[string]int{},
	}
	sender := &mockSenderFunc{sendFn: func(email gsmail.Email) error {
		rcpt := envelopeOf(email)
		h.attempts[rcpt]++
		if queue := h.replies[rcpt]; len(queue) > 0 {
			h.replies[rcpt] = queue[1:]
			return queue[0]
		}
		return nil
	}}
	provider := &providerEntities.EmailProvider{
		ID: testProviderID, Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
	}
	send := NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: provider},
		EventUsecase:    h.events,
		ProviderFactory: &mockFactory{sender: sender},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
	})
	h.worker = NewQueueWorker(h.outbox, send, h.suppress, &mockTenantUsecase{}, time.Second).(*queueWorker)

	req := panmailv1.SendEmailRequest{
		ProviderId: testProviderID, From: "noreply@example.com", To: to,
		Subject: "Your receipt", Body: "x",
	}
	raw, _ := protojson.Marshal(&req)
	h.outbox.emails = []*entities.OutboxEmail{{
		ID: "msg-1", TenantID: testTenantID, Request: raw,
		Status: entities.OutboxStatusPending, NextRetryAt: time.Now(),
	}}
	return h
}

// pass runs one worker pass and returns the row as the worker left it; nil if
// the worker deleted it.
func (h *outcomeHarness) pass() *entities.OutboxEmail {
	h.t.Helper()
	before := len(h.outbox.emails)
	h.outbox.lastUpdate = nil
	h.worker.processPending(context.Background())
	if h.outbox.lastUpdate == nil {
		if len(h.outbox.emails) < before {
			return nil
		}
		h.t.Fatal("the worker neither updated nor deleted the row")
	}
	row := h.outbox.lastUpdate
	// Make it due again for the next pass, as the clock would.
	row.NextRetryAt = time.Now()
	if row.Status == entities.OutboxStatusDeferred {
		row.Status = entities.OutboxStatusPending
	}
	h.outbox.emails = []*entities.OutboxEmail{row}
	return row
}

func (h *outcomeHarness) eventsFor(recipient string, types ...panmailv1.EmailEventType) int {
	want := map[panmailv1.EmailEventType]bool{}
	for _, ty := range types {
		want[ty] = true
	}
	n := 0
	for _, e := range h.events.events {
		if e.Recipient == recipient && want[e.Type] {
			n++
		}
	}
	return n
}

var failureTypes = []panmailv1.EmailEventType{
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED,
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED,
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED,
}

const (
	hardBounce = "550 5.1.1 user unknown"
	softBounce = "421 4.7.0 try again later"
)

// The worst of these. When every recipient failed, the worker classified the
// joined errors once, and one 550 made the whole thing a hard bounce --
// recorded against every recipient, and since a hard bounce suppresses, a
// co-recipient whose only problem was "try again later" was suppressed for
// good and never got the message.
func TestOneHardBounceDoesNotConvictItsCoRecipients(t *testing.T) {
	h := newOutcomeHarness(t, "gone@example.com", "busy@example.com")
	h.replies["gone@example.com"] = []error{errors.New(hardBounce)}
	h.replies["busy@example.com"] = []error{errors.New(softBounce)}

	row := h.pass()

	if h.eventsFor("gone@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE) != 1 {
		t.Error("the recipient that hard-bounced has no HARD_BOUNCE of its own")
	}
	if n := h.eventsFor("busy@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE); n != 0 {
		t.Fatalf("busy@ was recorded as hard-bounced %d time(s) for a co-recipient's 550 -- that suppresses them", n)
	}
	if row == nil || row.Status != entities.OutboxStatusPending {
		t.Fatalf("row = %+v, want it kept for busy@, who is worth another attempt", row)
	}
}

// A recipient that failed while others succeeded was never tried again: the
// pass read as success and the row was deleted, while that recipient's last
// event said DEFERRED.
func TestATransientFailureIsRetriedForThatRecipientAlone(t *testing.T) {
	h := newOutcomeHarness(t, "ok@example.com", "busy@example.com")
	h.replies["busy@example.com"] = []error{errors.New(softBounce)}

	if row := h.pass(); row == nil {
		t.Fatal("the row was deleted with busy@ still undelivered")
	}
	if row := h.pass(); row != nil {
		t.Fatalf("row = %+v, want it deleted once busy@ is delivered", row)
	}

	if h.attempts["ok@example.com"] != 1 {
		t.Errorf("ok@ was sent %d times; the retry must not resend to a delivered recipient", h.attempts["ok@example.com"])
	}
	if h.eventsFor("busy@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED) != 1 {
		t.Error("busy@ was never delivered on the retry")
	}
}

// A hard bounce alongside successes used to be recorded only as DEFERRED, so it
// never reached the suppression list.
func TestAPermanentFailureAlongsideSuccessesIsRecordedAsSuch(t *testing.T) {
	h := newOutcomeHarness(t, "ok@example.com", "gone@example.com")
	h.replies["gone@example.com"] = []error{errors.New(hardBounce)}

	if row := h.pass(); row != nil {
		t.Fatalf("row = %+v, want it done: nothing is worth retrying", row)
	}
	if h.eventsFor("gone@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE) != 1 {
		t.Error("gone@ has no HARD_BOUNCE, so it would never be suppressed")
	}
	if n := h.eventsFor("ok@example.com", failureTypes...); n != 0 {
		t.Errorf("ok@ was delivered and then recorded as failing %d time(s)", n)
	}
}

// A recipient finished for good in an earlier pass is not tried again when a
// co-recipient's transient failure keeps the message alive.
func TestARetryDoesNotRetryARecipientSettledEarlier(t *testing.T) {
	h := newOutcomeHarness(t, "gone@example.com", "busy@example.com")
	h.replies["gone@example.com"] = []error{errors.New(hardBounce)}
	h.replies["busy@example.com"] = []error{errors.New(softBounce)}

	h.pass()
	if row := h.pass(); row != nil {
		t.Fatalf("row = %+v, want it deleted once busy@ is delivered", row)
	}
	if h.attempts["gone@example.com"] != 1 {
		t.Errorf("gone@ was attempted %d times after it had hard-bounced", h.attempts["gone@example.com"])
	}
}

// Out of retries, the final verdict goes to the recipients still failing and
// to no one else.
func TestExhaustedRetriesSettleOnlyTheFailingRecipient(t *testing.T) {
	h := newOutcomeHarness(t, "ok@example.com", "busy@example.com")
	stuck := make([]error, 0, len(defaultRetryPattern)+2)
	for range len(defaultRetryPattern) + 2 {
		stuck = append(stuck, errors.New(softBounce))
	}
	h.replies["busy@example.com"] = stuck

	var row *entities.OutboxEmail
	for range len(defaultRetryPattern) + 2 {
		row = h.pass()
		if row == nil || row.Status == entities.OutboxStatusFailed {
			break
		}
	}
	if row == nil || row.Status != entities.OutboxStatusFailed {
		t.Fatalf("row = %+v, want it failed once the schedule ran out", row)
	}
	if h.eventsFor("busy@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE) != 1 {
		t.Error("busy@ has no final verdict")
	}
	if n := h.eventsFor("ok@example.com", failureTypes...); n != 0 {
		t.Errorf("ok@ was delivered and then given %d failure verdict(s)", n)
	}
}

// Every recipient failing for good fails the message -- each on its own
// verdict and its own error.
//
// The old path classified the joined errors once, and whichever pattern matched
// first decided everyone. Here that was the spam rejection, so a@, whose only
// problem was "user unknown", was recorded as a spam complaint. Complaint rate
// is a number tenants are judged on; recording complaints nobody made is not a
// labelling slip.
func TestEveryRecipientFailingPermanentlyFailsTheMessage(t *testing.T) {
	const spam = "550 5.7.1 message rejected as spam"
	h := newOutcomeHarness(t, "a@example.com", "b@example.com")
	h.replies["a@example.com"] = []error{errors.New(hardBounce)}
	h.replies["b@example.com"] = []error{errors.New(spam)}

	row := h.pass()
	if row == nil || row.Status != entities.OutboxStatusFailed {
		t.Fatalf("row = %+v, want failed", row)
	}

	verdicts := map[string][]string{}
	for _, e := range h.events.events {
		switch e.Type {
		case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT:
			verdicts[e.Recipient] = append(verdicts[e.Recipient], e.Type.String()+" | "+e.ErrorMessage)
		}
	}

	wantA := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE.String() + " | " + hardBounce
	if got := verdicts["a@example.com"]; len(got) != 1 || got[0] != wantA {
		t.Errorf("a@ verdicts = %q, want exactly its own hard bounce", got)
	}
	wantB := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT.String() + " | " + spam
	if got := verdicts["b@example.com"]; len(got) != 1 || got[0] != wantB {
		t.Errorf("b@ verdicts = %q, want exactly its own spam rejection", got)
	}
}

// An unsubscribe carried in an SMTP reply is suppressed by the worker -- for
// that recipient, not for everyone on the message.
func TestAnUnsubscribeReplySuppressesOnlyThatRecipient(t *testing.T) {
	h := newOutcomeHarness(t, "ok@example.com", "left@example.com")
	h.replies["left@example.com"] = []error{errors.New("550 recipient has unsubscribed")}

	h.pass()
	if len(h.suppress.suppressedEmails) != 1 || h.suppress.suppressedEmails[0] != "left@example.com" {
		t.Fatalf("suppressed %v, want only left@example.com", h.suppress.suppressedEmails)
	}
}
