package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
	"github.com/gsoultan/panmail/internal/storetest"
)

var providerA = storetest.ID("provider-a")

func newReceipts(t *testing.T) *receiptStore {
	t.Helper()
	return NewReceiptStore(storetest.NewConnection(t)).(*receiptStore)
}

func TestASecondClaimOfACommittedMessageIsADuplicate(t *testing.T) {
	ctx := context.Background()
	s := newReceipts(t)

	got, token, err := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute)
	if err != nil || got != stores.ClaimAcquired || token == "" {
		t.Fatalf("first claim = %v, %q, %v; want acquired with a token", got, token, err)
	}
	if got, _, _ := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute); got != stores.ClaimInFlight {
		t.Fatalf("claim while the first is processing = %v, want in flight", got)
	}

	if err := s.Complete(ctx, storetest.TenantA, providerA, "k", token); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got, _, _ := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute); got != stores.ClaimDuplicate {
		t.Fatalf("claim after completion = %v, want duplicate", got)
	}
}

// A receipt is per tenant and provider: two tenants receiving the same
// broadcast, or one tenant receiving it through two providers, each store it.
func TestClaimsAreScopedToTenantAndProvider(t *testing.T) {
	ctx := context.Background()
	s := newReceipts(t)
	providerB := storetest.ID("provider-b")

	for _, c := range []struct{ tenant, provider string }{
		{storetest.TenantA, providerA},
		{storetest.TenantB, providerA},
		{storetest.TenantA, providerB},
	} {
		if got, _, err := s.Claim(ctx, c.tenant, c.provider, "k", time.Minute); err != nil || got != stores.ClaimAcquired {
			t.Errorf("claim for %s/%s = %v, %v; want acquired", c.tenant, c.provider, got, err)
		}
	}
}

// A post that fails gives its claim back, so the provider's retry is
// processed instead of answered as a duplicate of a message never stored.
func TestAReleasedClaimCanBeClaimedAgain(t *testing.T) {
	ctx := context.Background()
	s := newReceipts(t)

	_, token, _ := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute)
	if err := s.Release(ctx, storetest.TenantA, providerA, "k", token); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got, _, _ := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute); got != stores.ClaimAcquired {
		t.Fatalf("claim after release = %v, want acquired", got)
	}
}

// A post that died holding a claim must not lose the message for good: once
// the lease runs out its retry takes the claim over. The dead post's token no
// longer settles anything, so if it was only slow it cannot mark the
// replacement's work done or throw it away.
func TestAStaleClaimIsTakenOverAndTheOldTokenSettlesNothing(t *testing.T) {
	ctx := context.Background()
	s := newReceipts(t)
	now := time.Now()
	s.now = func() time.Time { return now }

	_, stale, _ := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute)

	now = now.Add(2 * time.Minute)
	got, current, err := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute)
	if err != nil || got != stores.ClaimAcquired || current == stale {
		t.Fatalf("claim after the lease = %v, %v (same token: %v); want acquired with a new token",
			got, err, current == stale)
	}

	if err := s.Release(ctx, storetest.TenantA, providerA, "k", stale); err != nil {
		t.Fatalf("release with the stale token: %v", err)
	}
	if err := s.Complete(ctx, storetest.TenantA, providerA, "k", stale); err != nil {
		t.Fatalf("complete with the stale token: %v", err)
	}
	if got, _, _ := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute); got != stores.ClaimInFlight {
		t.Fatalf("after the stale token settled = %v, want the new claim still in flight", got)
	}

	if err := s.Complete(ctx, storetest.TenantA, providerA, "k", current); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got, _, _ := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute); got != stores.ClaimDuplicate {
		t.Fatalf("after completion = %v, want duplicate", got)
	}
}

// Concurrent posts of one message: exactly one may process it. This is what
// the primary key is for; a read-then-write check lets every one of them in.
func TestConcurrentClaimsAdmitExactlyOne(t *testing.T) {
	ctx := context.Background()
	s := newReceipts(t)

	const posts = 16
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		acquired int
	)
	for range posts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _, err := s.Claim(ctx, storetest.TenantA, providerA, "k", time.Minute)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if got == stores.ClaimAcquired {
				mu.Lock()
				acquired++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if acquired != 1 {
		t.Fatalf("%d of %d concurrent claims were acquired, want exactly 1", acquired, posts)
	}
}

func TestPruneRemovesOnlyOlderReceipts(t *testing.T) {
	ctx := context.Background()
	s := newReceipts(t)
	now := time.Now()

	s.now = func() time.Time { return now.Add(-10 * 24 * time.Hour) }
	_, old, _ := s.Claim(ctx, storetest.TenantA, providerA, "old", time.Minute)
	_ = s.Complete(ctx, storetest.TenantA, providerA, "old", old)

	s.now = func() time.Time { return now }
	_, fresh, _ := s.Claim(ctx, storetest.TenantA, providerA, "fresh", time.Minute)
	_ = s.Complete(ctx, storetest.TenantA, providerA, "fresh", fresh)

	removed, err := s.PruneBefore(ctx, now.Add(-7*24*time.Hour))
	if err != nil || removed != 1 {
		t.Fatalf("prune = %d, %v; want 1", removed, err)
	}
	if got, _, _ := s.Claim(ctx, storetest.TenantA, providerA, "fresh", time.Minute); got != stores.ClaimDuplicate {
		t.Errorf("the fresh receipt was pruned: claim = %v", got)
	}
	if got, _, _ := s.Claim(ctx, storetest.TenantA, providerA, "old", time.Minute); got != stores.ClaimAcquired {
		t.Errorf("the old receipt survived: claim = %v", got)
	}
}
