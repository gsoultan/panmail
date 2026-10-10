package usecases

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/pkg/tracking"
	"google.golang.org/protobuf/encoding/protojson"
)

// slowSender takes a while over every recipient, as a real SMTP server does,
// and notes how much time each attempt was given.
type slowSender struct {
	per time.Duration

	mu        sync.Mutex
	remaining []time.Duration
	delivered []string
}

func (s *slowSender) Send(ctx context.Context, email gsmail.Email) error {
	deadline, ok := ctx.Deadline()
	s.mu.Lock()
	if ok {
		s.remaining = append(s.remaining, time.Until(deadline))
	} else {
		s.remaining = append(s.remaining, -1)
	}
	s.mu.Unlock()

	select {
	case <-time.After(s.per):
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.delivered = append(s.delivered, envelopeOf(email))
	s.mu.Unlock()
	return nil
}
func (s *slowSender) Validate(context.Context, string) error { return nil }
func (s *slowSender) Ping(context.Context) error             { return nil }
func (s *slowSender) SetRetryConfig(gsmail.RetryConfig)      {}

func slowHarness(t *testing.T, sender *slowSender, recipients int) (*queueWorker, *sendEmailUsecase, *workerMockOutboxRepo) {
	t.Helper()
	send := NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo: &mockProviderRepo{provider: &providerEntities.EmailProvider{
			ID: testProviderID, Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		}},
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: sender},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
	}).(*sendEmailUsecase)
	outbox := &workerMockOutboxRepo{}
	worker := NewQueueWorker(outbox, send, &mockSuppressionUsecase{}, &mockTenantUsecase{}, time.Second).(*queueWorker)

	to := make([]string, recipients)
	for i := range to {
		to[i] = fmt.Sprintf("r%02d@example.com", i)
	}
	raw, _ := protojson.Marshal(&panmailv1.SendEmailRequest{
		ProviderId: testProviderID, From: "noreply@example.com", To: to, Subject: "x", Body: "x",
	})
	outbox.emails = []*entities.OutboxEmail{{
		ID: "msg-1", TenantID: testTenantID, Request: raw,
		Status: entities.OutboxStatusPending, NextRetryAt: time.Now(),
	}}
	return worker, send, outbox
}

// Every attempt gets the full attempt timeout, however many recipients came
// before it. The whole message used to share one 30-second budget, so each
// recipient was given whatever the ones before it had left: forty recipients at
// a second each delivered twenty-nine, and the rest of every pass was spent
// on the retry schedule.
func TestEachRecipientGetsItsOwnTimeout(t *testing.T) {
	sender := &slowSender{per: 30 * time.Millisecond}
	worker, _, _ := slowHarness(t, sender, 5)

	worker.processPending(context.Background())

	if len(sender.remaining) != 5 {
		t.Fatalf("%d attempts, want 5", len(sender.remaining))
	}
	for i, left := range sender.remaining {
		if left < deliveryAttemptTimeout-50*time.Millisecond {
			t.Errorf("recipient %d was given %s of a %s attempt timeout; it inherited what earlier recipients left",
				i, left, deliveryAttemptTimeout)
		}
	}
}

// And so a message whose recipients together take longer than any one
// attempt's timeout is delivered to all of them in one pass.
func TestAMessageLongerThanOneTimeoutIsDeliveredInOnePass(t *testing.T) {
	sender := &slowSender{per: 20 * time.Millisecond}
	worker, send, outbox := slowHarness(t, sender, 10)
	send.attemptTimeout = 100 * time.Millisecond // the ten together take 200ms

	worker.processPending(context.Background())

	if len(sender.delivered) != 10 {
		t.Fatalf("delivered %d of 10 recipients in one pass", len(sender.delivered))
	}
	if len(outbox.emails) != 0 {
		t.Errorf("row = %+v, want it deleted: everyone was delivered", outbox.lastUpdate)
	}
}

// An attempt that runs past its own timeout fails, for that recipient alone.
func TestAnAttemptThatRunsOverFailsOnlyThatRecipient(t *testing.T) {
	sender := &slowSender{per: 50 * time.Millisecond}
	worker, send, outbox := slowHarness(t, sender, 2)
	send.attemptTimeout = 10 * time.Millisecond

	worker.processPending(context.Background())

	if len(sender.delivered) != 0 {
		t.Fatalf("delivered %v; every attempt should have timed out", sender.delivered)
	}
	if outbox.lastUpdate == nil || outbox.lastUpdate.Status != entities.OutboxStatusDeferred {
		t.Fatalf("row = %+v, want it deferred for another attempt", outbox.lastUpdate)
	}
}

// A pass that delivered to someone is progress, and does not spend a retry.
// Spending one bounded how many recipients a slow message could ever reach by
// the length of the retry schedule.
func TestAPassThatDeliveredToSomeoneDoesNotSpendARetry(t *testing.T) {
	h := newOutcomeHarness(t, "a@example.com", "b@example.com", "c@example.com")
	h.replies["b@example.com"] = []error{errors.New(softBounce)}
	h.replies["c@example.com"] = []error{errors.New(softBounce), errors.New(softBounce), errors.New(softBounce)}

	// a@ delivered; b@ and c@ deferred.
	if row := h.pass(); row == nil || row.RetryCount != 0 {
		t.Fatalf("after a pass that delivered a@: row = %+v, want retry count 0", row)
	}
	// b@ delivered; c@ deferred again.
	if row := h.pass(); row == nil || row.RetryCount != 0 {
		t.Fatalf("after a pass that delivered b@: row = %+v, want retry count 0", row)
	}
	// Nobody delivered: that is an attempt.
	row := h.pass()
	if row == nil || row.RetryCount != 1 {
		t.Fatalf("after a pass that delivered no one: row = %+v, want retry count 1", row)
	}
	if row.Status != entities.OutboxStatusPending { // the harness turns DEFERRED back into PENDING
		t.Fatalf("status = %s, want it kept for c@", row.Status)
	}
}
