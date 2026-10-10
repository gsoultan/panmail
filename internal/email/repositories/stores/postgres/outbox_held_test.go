package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/storetest"
)

// HELD rows were never deleted by anything: PruneTerminal takes only FAILED,
// and an expiry only flipped the review record. PruneHeld is the backstop, and
// it must never take a payload a reviewer can still release.
func TestPruneHeldRemovesOnlyPayloadsNoReviewCanRelease(t *testing.T) {
	ctx := context.Background()
	repo, sqlDB := newOutboxTestStore(t)
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	cutoff := now.Add(-24 * time.Hour)

	row := func(name string, status entities.OutboxStatus, created time.Time) string {
		t.Helper()
		id := storetest.ID(name)
		if err := repo.Create(ctx, &entities.OutboxEmail{
			ID: id, TenantID: storetest.TenantA, Request: []byte(`{}`),
			Status: status, NextRetryAt: created, CreatedAt: created, UpdatedAt: created,
		}); err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
		return id
	}
	review := func(name, messageID, status string) {
		t.Helper()
		if _, err := sqlDB.ExecContext(ctx, `
			INSERT INTO filtered_messages
			    (id, tenant_id, direction, rule_name, action, status, message_id, payload_ref,
			     from_address, recipients, matched, created_at)
			VALUES ($1, $2, 'outbound', 'r', 'hold', $3, $4, $5, 'a@example.com', '[]', '[]', $6)`,
			storetest.ID(name), storetest.TenantA, status, messageID, messageID, old.UTC()); err != nil {
			t.Fatalf("seeding review %s: %v", name, err)
		}
	}

	expired := row("expired", entities.OutboxStatusHeld, old)
	review("r-expired", expired, "EXPIRED")
	rejected := row("rejected", entities.OutboxStatusHeld, old)
	review("r-rejected", rejected, "REJECTED")
	orphan := row("orphan", entities.OutboxStatusHeld, old) // its review was never written

	waiting := row("waiting", entities.OutboxStatusHeld, old) // a reviewer can still release it
	review("r-waiting", waiting, "PENDING")
	recent := row("recent", entities.OutboxStatusHeld, now)
	review("r-recent", recent, "EXPIRED")
	pending := row("pending", entities.OutboxStatusPending, old) // live work, not held

	removed, err := repo.PruneHeld(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneHeld: %v", err)
	}
	if removed != 3 {
		t.Errorf("removed %d rows, want 3", removed)
	}

	for id, wantKept := range map[string]bool{
		expired: false, rejected: false, orphan: false,
		waiting: true, recent: true, pending: true,
	} {
		got, err := repo.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if kept := got != nil; kept != wantKept {
			t.Errorf("row %s kept = %v, want %v", id, kept, wantKept)
		}
	}
}
