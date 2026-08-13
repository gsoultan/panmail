package postgres

import (
	"context"
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
	d := &entities.WebhookDelivery{
		ID:            uuid.New().String(),
		TenantID:      tenantID,
		WebhookID:     uuid.New().String(),
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

	claimed, err := repo.ClaimDue(ctx, 10, time.Minute)
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

	first, _ := repo.ClaimDue(ctx, 10, time.Minute)
	second, _ := repo.ClaimDue(ctx, 10, time.Minute)

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
	if _, err := repo.ClaimDue(ctx, 10, -time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}

	reclaimed, err := repo.ClaimDue(ctx, 10, time.Minute)
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

	claimed, err := repo.ClaimDue(context.Background(), 10, time.Minute)
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
	claimed, _ := repo.ClaimDue(ctx, 10, time.Hour)

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
	again, err := repo.ClaimDue(ctx, 10, time.Minute)
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
	repo := newDeliveryTestStore(t)
	ctx := context.Background()

	queue(t, repo, storetest.TenantA, time.Now().Add(-time.Minute))
	claimed, _ := repo.ClaimDue(ctx, 10, time.Minute)

	for _, status := range []entities.DeliveryStatus{entities.DeliveryStatusDelivered, entities.DeliveryStatusFailed} {
		d := claimed[0]
		d.Status = status
		d.NextAttemptAt = time.Now().Add(-time.Hour)
		if err := repo.Update(ctx, d); err != nil {
			t.Fatalf("update: %v", err)
		}

		again, _ := repo.ClaimDue(ctx, 10, time.Minute)
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

	claimed, err := repo.ClaimDue(ctx, 2, time.Minute)
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

	finished := queue(t, repo, storetest.TenantA, old)
	finished.Status = entities.DeliveryStatusDelivered
	finished.UpdatedAt = old
	repo.Update(ctx, finished)

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
	claimed, _ := repo.ClaimDue(ctx, 10, time.Minute)
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

	claimed, err := repo.ClaimDue(context.Background(), 10, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Errorf("claimed %d; a notification written in local time is invisible to the worker", len(claimed))
	}
}
