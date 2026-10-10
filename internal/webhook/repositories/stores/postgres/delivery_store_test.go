package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores"
)

func newDeliveryTestStore(t *testing.T) stores.DeliveryRepository {
	t.Helper()
	return NewDeliveryStore(storetest.NewConnection(t))
}

func queue(t *testing.T, repo stores.DeliveryRepository, tenantID string, at time.Time) *entities.WebhookDelivery {
	t.Helper()
	return queueFor(t, repo, tenantID, uuid.New().String(), at)
}

// queueFor queues a notification owed to a particular subscription.
func queueFor(t *testing.T, repo stores.DeliveryRepository, tenantID, webhookID string, at time.Time) *entities.WebhookDelivery {
	t.Helper()
	d := &entities.WebhookDelivery{
		ID:            uuid.New().String(),
		TenantID:      tenantID,
		WebhookID:     webhookID,
		Event:         "WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED",
		Payload:       []byte(`{"event":"bounced"}`),
		Status:        entities.DeliveryStatusPending,
		NextAttemptAt: at,
	}
	if err := repo.Create(context.Background(), d); err != nil {
		t.Fatalf("create: %v", err)
	}
	return d
}

func TestADueNotificationIsClaimed(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	queue(t, repo, storetest.TenantA, time.Now().Add(-time.Minute))

	claimed, err := repo.ClaimDue(ctx, 10, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d notifications, want 1", len(claimed))
	}
	if string(claimed[0].Payload) != `{"event":"bounced"}` {
		t.Errorf("payload came back as %q", claimed[0].Payload)
	}
}

// Two workers polling the same table must never both take one notification, or
// the tenant's endpoint is called twice for one event — and a consumer that
// opens a ticket per webhook opens two.
func TestAClaimedNotificationIsNotClaimedAgain(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	queue(t, repo, storetest.TenantA, time.Now().Add(-time.Minute))

	first, _ := repo.ClaimDue(ctx, 10, 10, time.Minute)
	second, _ := repo.ClaimDue(ctx, 10, 10, time.Minute)

	if len(first) != 1 {
		t.Fatalf("first claim took %d", len(first))
	}
	if len(second) != 0 {
		t.Errorf("second claim took %d; the lease is not holding", len(second))
	}
}

// A worker that dies mid-delivery would otherwise strand the notification.
func TestAnExpiredClaimIsReclaimed(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	queue(t, repo, storetest.TenantA, time.Now().Add(-time.Minute))

	// A lease that has already run out, as a crashed worker would leave it.
	if _, err := repo.ClaimDue(ctx, 10, 10, -time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}

	reclaimed, err := repo.ClaimDue(ctx, 10, 10, time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if len(reclaimed) != 1 {
		t.Errorf("reclaimed %d; a notification left by a dead worker is stranded", len(reclaimed))
	}
}

func TestANotificationScheduledForLaterIsNotClaimed(t *testing.T) {
	repo := newDeliveryTestStore(t)

	queue(t, repo, storetest.TenantA, time.Now().Add(time.Hour))

	claimed, err := repo.ClaimDue(context.Background(), 10, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d before its retry was due", len(claimed))
	}
}

func TestAnOutcomeIsRecordedAndReleasesTheClaim(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	queue(t, repo, storetest.TenantA, time.Now().Add(-time.Minute))
	claimed, _ := repo.ClaimDue(ctx, 10, 10, time.Hour)

	d := claimed[0]
	d.Status = entities.DeliveryStatusDeferred
	d.AttemptCount = 1
	d.LastError = "endpoint returned 500"
	d.NextAttemptAt = time.Now().Add(-time.Second)
	if err := repo.Update(ctx, d); err != nil {
		t.Fatalf("update: %v", err)
	}

	// A row left claimed after the worker finished with it is invisible until
	// the lease expires — an hour here, which is why the update must release.
	again, err := repo.ClaimDue(ctx, 10, 10, time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("the deferred notification was not picked up again")
	}
	if again[0].AttemptCount != 1 || again[0].LastError != "endpoint returned 500" {
		t.Errorf("the outcome was not persisted: %+v", again[0])
	}
}

func TestATerminalNotificationIsNotRedelivered(t *testing.T) {
	ctx := context.Background()

	// A row per status: an outcome is written under a claim, and recording
	// one releases it, so the same row cannot be finished twice.
	for _, status := range []entities.DeliveryStatus{entities.DeliveryStatusDelivered, entities.DeliveryStatusFailed} {
		repo := newDeliveryTestStore(t)
		queue(t, repo, storetest.TenantA, time.Now().Add(-time.Minute))
		claimed, _ := repo.ClaimDue(ctx, 10, 10, time.Minute)

		d := claimed[0]
		d.Status = status
		d.NextAttemptAt = time.Now().Add(-time.Hour)
		if err := repo.Update(ctx, d); err != nil {
			t.Fatalf("update: %v", err)
		}

		again, _ := repo.ClaimDue(ctx, 10, 10, time.Minute)
		if len(again) != 0 {
			t.Errorf("a %s notification was claimed again", status)
		}
	}
}

func TestClaimingRespectsTheBatchSize(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	for range 5 {
		queue(t, repo, storetest.TenantA, time.Now().Add(-time.Minute))
	}

	claimed, err := repo.ClaimDue(ctx, 2, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Errorf("claimed %d, want the batch size of 2", len(claimed))
	}
}

func TestPruneRemovesFinishedNotificationsOnly(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	old := time.Now().Add(-48 * time.Hour)

	// Finished the way the worker finishes one: claimed, then recorded. An
	// outcome can only be written under a claim.
	queue(t, repo, storetest.TenantA, old)
	claimed, err := repo.ClaimDue(ctx, 10, 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %d rows, %v", len(claimed), err)
	}
	finished := claimed[0]
	finished.Status = entities.DeliveryStatusDelivered
	if err := repo.Update(ctx, finished); err != nil {
		t.Fatalf("update: %v", err)
	}

	pending := queue(t, repo, storetest.TenantA, old)

	// Update stamps its own UpdatedAt, so the cutoff has to be now for the
	// delivered row to qualify.
	removed, err := repo.PruneTerminal(ctx, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d rows, want only the finished one", removed)
	}

	// The pending one is still live work; pruning it loses a notification.
	claimed, _ = repo.ClaimDue(ctx, 10, 10, time.Minute)
	if len(claimed) != 1 || claimed[0].ID != pending.ID {
		t.Error("the pending notification was pruned")
	}
}

// The bug that made the queue look asleep.
//
// SQLite stores whatever offset the driver renders and compares timestamps as
// strings, so a row written in local time is never found by a query asking for
// "<= now" in UTC — the same instant, ordered wrongly. Nothing was ever
// claimed, and nothing said why.
func TestATimestampInLocalTimeIsStillFound(t *testing.T) {
	repo := newDeliveryTestStore(t)

	// Explicitly not UTC, as a caller would naturally write it.
	local := time.Now().In(time.FixedZone("UTC+7", 7*3600)).Add(-time.Minute)
	queue(t, repo, storetest.TenantA, local)

	claimed, err := repo.ClaimDue(context.Background(), 10, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Errorf("claimed %d; a notification written in local time is invisible to the worker", len(claimed))
	}
}

// The double delivery this closes. Worker A's lease ran out mid-batch, worker
// B reclaimed the row and delivered it, and A then wrote its own outcome over
// B's -- unconditionally, so a row B had marked delivered could go back to
// deferred and be sent a third time.
func TestAnOutcomeIsRefusedOnceTheClaimHasMovedOn(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	queue(t, repo, storetest.TenantA, time.Now().Add(-time.Minute))

	// A's lease, already run out.
	stale, err := repo.ClaimDue(ctx, 10, 10, -time.Minute)
	if err != nil || len(stale) != 1 {
		t.Fatalf("first claim: %d rows, %v", len(stale), err)
	}
	fresh, err := repo.ClaimDue(ctx, 10, 10, time.Minute)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("reclaim: %d rows, %v", len(fresh), err)
	}

	// B delivers and records it.
	fresh[0].Status = entities.DeliveryStatusDelivered
	fresh[0].AttemptCount = 1
	if err := repo.Update(ctx, fresh[0]); err != nil {
		t.Fatalf("B's update: %v", err)
	}

	// A, late, would put it back for another attempt.
	stale[0].Status = entities.DeliveryStatusDeferred
	stale[0].AttemptCount = 1
	stale[0].NextAttemptAt = time.Now().Add(-time.Second)
	if err := repo.Update(ctx, stale[0]); !errors.Is(err, stores.ErrClaimLost) {
		t.Fatalf("A's update returned %v, want ErrClaimLost", err)
	}

	if again, _ := repo.ClaimDue(ctx, 10, 10, time.Minute); len(again) != 0 {
		t.Errorf("a delivered notification was made due again by a worker that no longer held it")
	}
}

func TestAnOutcomeWithoutAClaimIsRefused(t *testing.T) {
	repo := newDeliveryTestStore(t)
	d := queue(t, repo, storetest.TenantA, time.Now().Add(-time.Minute))

	d.Status = entities.DeliveryStatusFailed
	if err := repo.Update(context.Background(), d); !errors.Is(err, stores.ErrClaimLost) {
		t.Errorf("update without a claim returned %v, want ErrClaimLost", err)
	}
}

// The starvation this closes: a subscription owning more than a batch's worth
// of the oldest due rows -- an endpoint that has been timing out and piling up
// retries -- took the whole batch, and a second subscription's notification
// waited behind it however many batches that took.
func TestOneSubscriptionCannotTakeTheWholeBatch(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	slow, other := uuid.New().String(), uuid.New().String()
	// Created first, so every one of them is older than other's.
	for range 20 {
		queueFor(t, repo, storetest.TenantA, slow, time.Now().Add(-time.Minute))
	}
	waiting := queueFor(t, repo, storetest.TenantB, other, time.Now().Add(-time.Minute))

	claimed, err := repo.ClaimDue(ctx, 5, 3, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	perSub := map[string]int{}
	found := false
	for _, d := range claimed {
		perSub[d.WebhookID]++
		found = found || d.ID == waiting.ID
	}
	if !found {
		t.Errorf("the other subscription's notification was not claimed; the batch was %v", perSub)
	}
	if perSub[slow] > 3 {
		t.Errorf("one subscription took %d rows of the batch, over its cap of 3", perSub[slow])
	}
}

// Fairness must not cost a subscription that is alone in the queue: it still
// gets up to its cap, oldest first.
func TestASubscriptionAloneIsClaimedUpToItsCapOldestFirst(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	sub := uuid.New().String()
	var queued []*entities.WebhookDelivery
	for i := range 6 {
		d := &entities.WebhookDelivery{
			ID: uuid.New().String(), TenantID: storetest.TenantA, WebhookID: sub,
			Event: "E", Payload: []byte(`{}`), Status: entities.DeliveryStatusPending,
			NextAttemptAt: time.Now().Add(-time.Minute),
			CreatedAt:     time.Now().Add(time.Duration(i-10) * time.Minute),
		}
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create: %v", err)
		}
		queued = append(queued, d)
	}

	claimed, err := repo.ClaimDue(ctx, 50, 4, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 4 {
		t.Fatalf("claimed %d, want the cap of 4", len(claimed))
	}
	for i, d := range claimed {
		if d.ID != queued[i].ID {
			t.Errorf("claim %d was not the subscription's %d-th oldest", i, i+1)
		}
	}
}

// Concurrent claimers on the same rows -- the case FOR UPDATE SKIP LOCKED is
// there for on PostgreSQL. No row may come back to two of them.
func TestConcurrentClaimsNeverShareARow(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	subs := []string{uuid.New().String(), uuid.New().String(), uuid.New().String()}
	for i := range 60 {
		queueFor(t, repo, storetest.TenantA, subs[i%len(subs)], time.Now().Add(-time.Minute))
	}

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			for range 5 {
				claimed, err := repo.ClaimDue(ctx, 4, 2, time.Minute)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				mu.Lock()
				for _, d := range claimed {
					seen[d.ID]++
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	for id, n := range seen {
		if n != 1 {
			t.Errorf("notification %s was claimed %d times", id, n)
		}
	}
}

func TestDeferringASubscriptionLeavesClaimedRowsAndOtherSubscriptionsAlone(t *testing.T) {
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	dead, other := uuid.New().String(), uuid.New().String()
	queueFor(t, repo, storetest.TenantA, dead, time.Now().Add(-time.Minute))
	held, err := repo.ClaimDue(ctx, 10, 10, time.Minute)
	if err != nil || len(held) != 1 {
		t.Fatalf("claim: %d rows, %v", len(held), err)
	}
	for range 3 {
		queueFor(t, repo, storetest.TenantA, dead, time.Now().Add(-time.Minute))
	}
	otherRow := queueFor(t, repo, storetest.TenantB, other, time.Now().Add(-time.Minute))

	if err := repo.DeferSubscription(ctx, dead, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("defer: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 10, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != otherRow.ID {
		t.Errorf("after deferring one subscription the claim took %d rows; want only the other subscription's", len(claimed))
	}

	// The row held when the subscription was deferred still belongs to its
	// claimer, who records the outcome.
	held[0].Status = entities.DeliveryStatusDelivered
	if err := repo.Update(ctx, held[0]); err != nil {
		t.Errorf("the claimer's update was refused after the deferral: %v", err)
	}
}
