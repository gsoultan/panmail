package postgres

import (
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/internal/storetest"
)

// Nothing ever deleted a filtered_messages row, decided or not, so the review
// history grew for the life of a deployment. PruneTerminal removes decisions
// that are over, by when they were decided, and never a hold still waiting.
func TestPruneTerminalRemovesOnlyDecisionsThatAreOver(t *testing.T) {
	ctx := t.Context()
	q := newQuarantine(t)
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	cutoff := now.Add(-24 * time.Hour)

	record := func(id string, action emailfilter.Action, created time.Time, expires *time.Time) {
		t.Helper()
		if err := q.Create(ctx, &emailfilter.FilteredMessage{
			ID: storetest.ID(id), TenantID: storetest.TenantA,
			Direction: emailfilter.DirectionOutbound, Action: action, Status: emailfilter.StatusPending,
			RuleName: "r", MessageID: storetest.ID("m-" + id),
			From: "a@example.com", Recipients: []string{"b@example.com"},
			CreatedAt: created, ExpiresAt: expires,
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	past := old.Add(time.Hour) // a deadline before the cutoff
	future := now.Add(time.Hour)

	// Over, and decided before the cutoff: pruned.
	record("old-reject", emailfilter.ActionReject, old, nil)
	record("old-tag", emailfilter.ActionTag, old, nil)
	record("old-expired", emailfilter.ActionHold, old, &past)
	if _, err := q.Expire(ctx, now, 500); err != nil {
		t.Fatalf("Expire: %v", err)
	}

	// Still waiting for a reviewer, however old: kept.
	record("old-pending-hold", emailfilter.ActionHold, old, nil)
	record("pending-hold", emailfilter.ActionHold, old, &future)

	// Over, but decided after the cutoff: kept for now.
	record("new-reject", emailfilter.ActionReject, now, nil)
	record("reviewed-recently", emailfilter.ActionHold, old, &future)
	if _, err := q.Review(ctx, storetest.TenantA, storetest.ID("reviewed-recently"),
		emailfilter.StatusRejected, "alice", ""); err != nil {
		t.Fatalf("Review: %v", err)
	}

	removed, err := q.PruneTerminal(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneTerminal: %v", err)
	}
	if removed != 3 {
		t.Errorf("removed %d rows, want 3", removed)
	}

	for id, wantKept := range map[string]bool{
		"old-reject": false, "old-tag": false, "old-expired": false,
		"old-pending-hold": true, "pending-hold": true,
		"new-reject": true, "reviewed-recently": true,
	} {
		_, err := q.Get(ctx, storetest.TenantA, storetest.ID(id))
		if kept := err == nil; kept != wantKept {
			t.Errorf("%s kept = %v, want %v (err %v)", id, kept, wantKept, err)
		}
	}
}
