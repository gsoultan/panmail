package usecases

import (
	"context"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"google.golang.org/protobuf/encoding/protojson"
)

// The outbox worker's half of delivery pacing: a paced message is rescheduled
// for its slot, never counted as an attempt. Its own file so the worker's other
// tests and these can change independently.

func pacedOutboxRow(t *testing.T, id string, retryCount int, lastError string) *entities.OutboxEmail {
	t.Helper()
	req := panmailv1.SendEmailRequest{To: []string{"a@example.com"}, From: "b@example.com", Subject: "x", Body: "x"}
	reqBytes, _ := protojson.Marshal(&req)
	return &entities.OutboxEmail{
		ID: id, TenantID: "tenant1", Request: reqBytes,
		Status: entities.OutboxStatusPending, NextRetryAt: time.Now(),
		RetryCount: retryCount, LastError: lastError,
	}
}

// Paced is not failed: the message was never attempted, only told its turn is
// later. It keeps its retry count, records no event, and keeps the last real
// error it had.
func TestAPacedMessageIsRescheduledNotCountedAsAnAttempt(t *testing.T) {
	outboxRepo := &workerMockOutboxRepo{}
	emailUsecase := &mockEmailUsecase{err: &DeliveryPacedError{RetryAfter: 42 * time.Second}}
	w := NewQueueWorker(outboxRepo, emailUsecase, &mockSuppressionUsecase{},
		&mockTenantUsecase{}, time.Second).(*queueWorker)

	outboxRepo.emails = append(outboxRepo.emails, pacedOutboxRow(t, "1", 2, "421 try later"))
	before := time.Now()
	w.processPending(context.Background())

	got := outboxRepo.lastUpdate
	if got == nil {
		t.Fatal("the outbox row was never updated")
	}
	if got.Status != entities.OutboxStatusDeferred {
		t.Errorf("status = %s, want deferred until its slot", got.Status)
	}
	if got.RetryCount != 2 {
		t.Errorf("retry count = %d, want the 2 it had: pacing is not an attempt", got.RetryCount)
	}
	if got.LastError != "421 try later" {
		t.Errorf("last error = %q, want the real one it had kept", got.LastError)
	}
	if wait := got.NextRetryAt.Sub(before); wait < 41*time.Second || wait > 44*time.Second {
		t.Errorf("next attempt in %v, want the 42s slot it was given", wait)
	}
	if len(emailUsecase.recorded) != 0 {
		t.Errorf("recorded %v, want no events for a message that was never attempted", emailUsecase.recorded)
	}
}

// Counting pacing as an attempt would let a large backlog exhaust the retry
// schedule on pacing alone and fail mail that nothing was wrong with.
func TestPacingNeverExhaustsTheRetrySchedule(t *testing.T) {
	outboxRepo := &workerMockOutboxRepo{}
	emailUsecase := &mockEmailUsecase{err: &DeliveryPacedError{RetryAfter: time.Second}}
	w := NewQueueWorker(outboxRepo, emailUsecase, &mockSuppressionUsecase{},
		&mockTenantUsecase{}, time.Second).(*queueWorker)

	row := pacedOutboxRow(t, "1", 0, "")
	for range 3 * len(defaultRetryPattern) {
		row.Status = entities.OutboxStatusPending
		row.NextRetryAt = time.Now()
		outboxRepo.emails = []*entities.OutboxEmail{row}
		w.processPending(context.Background())
		row = outboxRepo.lastUpdate
		if row.Status == entities.OutboxStatusFailed {
			t.Fatal("a message was failed on pacing alone")
		}
	}
	if row.RetryCount != 0 {
		t.Errorf("retry count = %d after being paced repeatedly, want 0", row.RetryCount)
	}
}
