package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/email/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
	_ "modernc.org/sqlite"
)

// newOutboxTestStore builds a store backed by a real on-disk SQLite database.
//
// The claim logic is the one place where being right depends on what the
// database actually does with concurrent writers, so this exercises the real
// SQL rather than a stub. It also demonstrates that the $N placeholders the
// embedded queries use work on SQLite.
func newOutboxTestStore(t *testing.T) (stores.OutboxRepository, *sql.DB) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "outbox_test.db")
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	schema := `
	CREATE TABLE outbox (
		id VARCHAR(36) PRIMARY KEY,
		tenant_id VARCHAR(36) NOT NULL,
		request TEXT NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
		retry_count INTEGER NOT NULL DEFAULT 0,
		next_retry_at DATETIME NOT NULL,
		last_error TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		claim_token VARCHAR(36),
		claimed_until DATETIME
	);`
	if _, err := sqlDB.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	return NewOutboxStore(db.NewConnection(sqlDB)), sqlDB
}

func seedPending(t *testing.T, repo stores.OutboxRepository, count int) {
	t.Helper()

	past := time.Now().Add(-time.Minute)
	for i := range count {
		err := repo.Create(context.Background(), &entities.OutboxEmail{
			ID:          fmt.Sprintf("message-%03d", i),
			TenantID:    "tenant-1",
			Request:     []byte(`{"from":"a@example.com"}`),
			Status:      entities.OutboxStatusPending,
			NextRetryAt: past,
			CreatedAt:   past,
			UpdatedAt:   past,
		})
		if err != nil {
			t.Fatalf("failed to seed outbox row %d: %v", i, err)
		}
	}
}

// The property the whole claim mechanism exists for: two workers polling the
// same table must never both receive the same message, because the recipient
// would get the email twice.
func TestClaimPendingNeverHandsOutTheSameRowTwice(t *testing.T) {
	const (
		rows    = 120
		workers = 8
		batch   = 25
	)

	repo, _ := newOutboxTestStore(t)
	seedPending(t, repo, rows)

	var (
		mu     sync.Mutex
		counts = make(map[string]int)
		total  int
	)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker drains until it comes up empty, mimicking the real
			// poll loop.
			for {
				claimed, err := repo.ClaimPending(context.Background(), batch, time.Minute)
				if err != nil {
					t.Errorf("ClaimPending failed: %v", err)
					return
				}
				if len(claimed) == 0 {
					return
				}

				mu.Lock()
				for _, e := range claimed {
					counts[e.ID]++
					total++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if total != rows {
		t.Errorf("claimed %d rows in total; want %d", total, rows)
	}

	for id, n := range counts {
		if n != 1 {
			t.Errorf("row %s was claimed %d times; want exactly 1", id, n)
		}
	}
	if len(counts) != rows {
		t.Errorf("%d distinct rows claimed; want %d", len(counts), rows)
	}
}

// A claimed row is not offered again while its lease is live, so a slow send
// does not get duplicated by the next poll.
func TestClaimPendingHidesLiveLeases(t *testing.T) {
	repo, _ := newOutboxTestStore(t)
	seedPending(t, repo, 3)

	first, err := repo.ClaimPending(context.Background(), 10, time.Hour)
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(first))
	}

	second, err := repo.ClaimPending(context.Background(), 10, time.Hour)
	if err != nil {
		t.Fatalf("second claim failed: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("expected no rows while leases are live, got %d", len(second))
	}
}

// A worker that dies mid-send must not strand mail: once its lease lapses the
// row becomes available again.
func TestClaimPendingReclaimsExpiredLeases(t *testing.T) {
	repo, _ := newOutboxTestStore(t)
	seedPending(t, repo, 2)

	// A lease that has already expired stands in for a crashed worker.
	if _, err := repo.ClaimPending(context.Background(), 10, -time.Minute); err != nil {
		t.Fatalf("first claim failed: %v", err)
	}

	reclaimed, err := repo.ClaimPending(context.Background(), 10, time.Minute)
	if err != nil {
		t.Fatalf("reclaim failed: %v", err)
	}
	if len(reclaimed) != 2 {
		t.Errorf("expected both stranded rows to be reclaimable, got %d", len(reclaimed))
	}
}

// Messages whose retry time has not arrived are left alone.
func TestClaimPendingSkipsFutureRetries(t *testing.T) {
	repo, _ := newOutboxTestStore(t)

	future := time.Now().Add(time.Hour)
	err := repo.Create(context.Background(), &entities.OutboxEmail{
		ID:          "deferred-1",
		TenantID:    "tenant-1",
		Request:     []byte(`{}`),
		Status:      entities.OutboxStatusDeferred,
		NextRetryAt: future,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to seed: %v", err)
	}

	claimed, err := repo.ClaimPending(context.Background(), 10, time.Minute)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("expected a future retry to be skipped, got %d rows", len(claimed))
	}
}

// Resolving a message releases its claim, so the row is not left holding a
// stale token.
func TestUpdateReleasesTheClaim(t *testing.T) {
	repo, sqlDB := newOutboxTestStore(t)
	seedPending(t, repo, 1)

	claimed, err := repo.ClaimPending(context.Background(), 1, time.Hour)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim failed: %v (%d rows)", err, len(claimed))
	}

	e := claimed[0]
	e.Status = entities.OutboxStatusDeferred
	e.RetryCount = 1
	e.NextRetryAt = time.Now().Add(time.Minute)
	e.UpdatedAt = time.Now()
	if err := repo.Update(context.Background(), e); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	var token sql.NullString
	if err := sqlDB.QueryRow("SELECT claim_token FROM outbox WHERE id = $1", e.ID).Scan(&token); err != nil {
		t.Fatalf("failed to read back: %v", err)
	}
	if token.Valid && token.String != "" {
		t.Errorf("expected the claim to be released, still held by %q", token.String)
	}
}

// Failures are the only outbox rows that accumulate: a delivered message is
// deleted outright, so without a cutoff this table grows for the life of the
// deployment, and each row carries the whole serialised request including the
// body.
func TestPruneTerminalRemovesOnlyOldFailures(t *testing.T) {
	repo, sqlDB := newOutboxTestStore(t)
	ctx := context.Background()

	now := time.Now()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-time.Hour)

	seed := func(id string, status entities.OutboxStatus, updated time.Time) {
		t.Helper()
		err := repo.Create(ctx, &entities.OutboxEmail{
			ID: id, TenantID: "tenant-1", Request: []byte(`{}`),
			Status: status, NextRetryAt: updated, CreatedAt: updated, UpdatedAt: updated,
		})
		if err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		// Create stamps its own timestamps, so the age has to be forced.
		if _, err := sqlDB.Exec(`UPDATE outbox SET status = ?, updated_at = ? WHERE id = ?`, string(status), updated, id); err != nil {
			t.Fatalf("age %s: %v", id, err)
		}
	}

	seed("old-failed", entities.OutboxStatusFailed, old)
	seed("recent-failed", entities.OutboxStatusFailed, recent)
	seed("old-pending", entities.OutboxStatusPending, old)
	seed("old-deferred", entities.OutboxStatusDeferred, old)
	seed("old-sending", entities.OutboxStatusSending, old)

	removed, err := repo.PruneTerminal(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d rows, want 1", removed)
	}

	survives := func(id string) bool {
		e, err := repo.GetByID(ctx, id)
		// The store reports a missing row as sql.ErrNoRows rather than a nil
		// result, so absence is an error here and not a nil check.
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		return e != nil
	}

	if survives("old-failed") {
		t.Error("an old failure should have been pruned")
	}
	// The recent one is the record an operator consults when asking why
	// something never arrived.
	if !survives("recent-failed") {
		t.Error("a recent failure was pruned; it is still worth reading")
	}
	// These are all still live work. Deleting any of them loses mail.
	for _, id := range []string{"old-pending", "old-deferred", "old-sending"} {
		if !survives(id) {
			t.Errorf("%s was pruned, which loses a message that was never delivered", id)
		}
	}
}

func TestPruneTerminalOnAnEmptyTableIsHarmless(t *testing.T) {
	repo, _ := newOutboxTestStore(t)
	removed, err := repo.PruneTerminal(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed %d from an empty table", removed)
	}
}
