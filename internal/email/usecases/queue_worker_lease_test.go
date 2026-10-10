package usecases

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	outboxstore "github.com/gsoultan/panmail/internal/email/repositories/stores/postgres"
	"github.com/gsoultan/panmail/internal/storetest"
	"google.golang.org/protobuf/encoding/protojson"
)

// countingSends counts the sends a worker starts.
type countingSends struct {
	mockEmailUsecase
	sends atomic.Int64
}

func (c *countingSends) SendEmail(ctx context.Context, tenantID string, req *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error) {
	c.sends.Add(1)
	return c.mockEmailUsecase.SendEmail(ctx, tenantID, req)
}

func leaseTestRequest(t *testing.T) []byte {
	t.Helper()
	raw, err := protojson.Marshal(&panmailv1.SendEmailRequest{
		To: []string{"someone@example.com"}, From: "sender@example.com", Subject: "x", Body: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The race this closes, against the real claim SQL. A worker claims a batch of
// 500 and runs 200 at once, so most of the batch waits for a slot -- and the
// lease, taken at claim time and never extended, can lapse while it waits.
// Another replica then claims the message and sends it, and when the first
// worker finally gets a slot it sends it too.
func TestAMessageClaimedElsewhereWhileWaitingIsNotSent(t *testing.T) {
	ctx := context.Background()
	outbox := outboxstore.NewOutboxStore(storetest.NewConnection(t))
	past := time.Now().Add(-time.Minute)
	if err := outbox.Create(ctx, &entities.OutboxEmail{
		ID: storetest.ID("waiting"), TenantID: storetest.TenantA, Request: leaseTestRequest(t),
		Status: entities.OutboxStatusPending, NextRetryAt: past, CreatedAt: past, UpdatedAt: past,
	}); err != nil {
		t.Fatal(err)
	}

	// Replica A claims; its lease lapses while the message waits for a slot.
	stale, err := outbox.ClaimPending(ctx, 500, -time.Second)
	if err != nil || len(stale) != 1 {
		t.Fatalf("A's claim = %d rows, %v", len(stale), err)
	}
	// Replica B claims it in the meantime, and owns it now.
	current, err := outbox.ClaimPending(ctx, 500, time.Minute)
	if err != nil || len(current) != 1 {
		t.Fatalf("B's claim = %d rows, %v", len(current), err)
	}

	send := &countingSends{}
	a := NewQueueWorker(outbox, send, &mockSuppressionUsecase{}, &mockTenantUsecase{}, time.Second).(*queueWorker)

	// A finally gets a slot.
	a.processEmail(ctx, stale[0])

	if n := send.sends.Load(); n != 0 {
		t.Fatalf("A sent a message B had claimed (%d send(s)); the recipient gets it twice", n)
	}
	row, err := outbox.GetByID(ctx, current[0].ID)
	if err != nil || row == nil {
		t.Fatalf("B's row = %+v, %v; A must not have settled it", row, err)
	}
	if row.Status != entities.OutboxStatusSending {
		t.Errorf("B's row is %s; A wrote to a row it no longer owned", row.Status)
	}
}

// A message that takes longer than one lease keeps it, rather than becoming
// claimable by another replica half-way through.
func TestALongSendKeepsItsLeaseAlive(t *testing.T) {
	outbox := &workerMockOutboxRepo{}
	w := NewQueueWorker(outbox, &mockEmailUsecase{delay: 150 * time.Millisecond},
		&mockSuppressionUsecase{}, &mockTenantUsecase{}, time.Second).(*queueWorker)
	w.leaseRenewEvery = 10 * time.Millisecond

	email := &entities.OutboxEmail{ID: "long", TenantID: "tenant1", Request: leaseTestRequest(t), ClaimToken: "a"}
	outbox.emails = []*entities.OutboxEmail{email}
	w.processEmail(context.Background(), email)

	// One to confirm the claim at the start, and several while it ran.
	if n := outbox.renewals.Load(); n < 4 {
		t.Fatalf("the lease was renewed %d time(s) during a send several renewal periods long", n)
	}
	if len(outbox.emails) != 0 {
		t.Error("the delivered message was not settled")
	}
}

// When a renewal says another worker owns the message, the send in progress is
// stopped and nothing is written: settling the row would overwrite the claim
// of the worker that now owns it.
func TestLosingTheClaimMidSendStopsItAndSettlesNothing(t *testing.T) {
	var calls atomic.Int64
	outbox := &workerMockOutboxRepo{renew: func() (bool, error) {
		// The claim is confirmed at the start, and lost at the first renewal.
		return calls.Add(1) == 1, nil
	}}
	w := NewQueueWorker(outbox, &mockEmailUsecase{delay: 5 * time.Second},
		&mockSuppressionUsecase{}, &mockTenantUsecase{}, time.Second).(*queueWorker)
	w.leaseRenewEvery = 10 * time.Millisecond

	email := &entities.OutboxEmail{ID: "lost", TenantID: "tenant1", Request: leaseTestRequest(t), ClaimToken: "a"}
	outbox.emails = []*entities.OutboxEmail{email}

	start := time.Now()
	w.processEmail(context.Background(), email)

	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the send ran on for %s after its claim was lost", took)
	}
	if outbox.lastUpdate != nil {
		t.Errorf("the row was updated after the claim was lost: %+v", outbox.lastUpdate)
	}
	if len(outbox.emails) != 1 {
		t.Error("the row was deleted after the claim was lost")
	}
}
