package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
)

// A renewal keeps a claim the caller still holds, and refuses one another
// worker has taken since -- which is what tells a worker whose lease lapsed
// while the message waited for a slot not to send it.
func TestRenewClaimHonoursOnlyTheCurrentClaim(t *testing.T) {
	ctx := context.Background()
	repo, _ := newOutboxTestStore(t)
	seedPending(t, repo, 1)

	// Worker A claims with a lease that has already run out.
	stale, err := repo.ClaimPending(ctx, 10, -time.Minute)
	if err != nil || len(stale) != 1 {
		t.Fatalf("first claim = %d rows, %v", len(stale), err)
	}
	if stale[0].ClaimToken == "" {
		t.Fatal("a claimed row came back without its claim token")
	}

	// Worker B takes it over.
	current, err := repo.ClaimPending(ctx, 10, time.Minute)
	if err != nil || len(current) != 1 {
		t.Fatalf("reclaim = %d rows, %v", len(current), err)
	}

	ok, err := repo.RenewClaim(ctx, stale[0].ID, stale[0].ClaimToken, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("renewing the stale claim: %v", err)
	}
	if ok {
		t.Fatal("a claim another worker had since taken was renewed; both would send")
	}

	ok, err = repo.RenewClaim(ctx, current[0].ID, current[0].ClaimToken, time.Now().Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("renewing the current claim = %v, %v; want true", ok, err)
	}
}

// A renewed lease is honoured by the claim query: a message whose lease was
// extended past its original expiry is not handed to another worker.
func TestARenewedLeaseIsNotReclaimed(t *testing.T) {
	ctx := context.Background()
	repo, _ := newOutboxTestStore(t)
	seedPending(t, repo, 1)

	claimed, err := repo.ClaimPending(ctx, 10, -time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d rows, %v", len(claimed), err)
	}
	if ok, err := repo.RenewClaim(ctx, claimed[0].ID, claimed[0].ClaimToken, time.Now().Add(time.Hour)); err != nil || !ok {
		t.Fatalf("renew = %v, %v", ok, err)
	}

	again, err := repo.ClaimPending(ctx, 10, time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(again) != 0 {
		t.Fatal("a renewed lease was reclaimed by another worker")
	}
}

// Settling a row clears its claim, so a late renewal does not resurrect it.
func TestRenewClaimAfterSettlingIsRefused(t *testing.T) {
	ctx := context.Background()
	repo, _ := newOutboxTestStore(t)
	seedPending(t, repo, 1)

	claimed, err := repo.ClaimPending(ctx, 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d rows, %v", len(claimed), err)
	}
	row := claimed[0]
	row.Status = entities.OutboxStatusDeferred
	if err := repo.Update(ctx, row); err != nil {
		t.Fatalf("update: %v", err)
	}
	if ok, err := repo.RenewClaim(ctx, row.ID, row.ClaimToken, time.Now().Add(time.Hour)); err != nil || ok {
		t.Fatalf("renew after settling = %v, %v; want false", ok, err)
	}
}
