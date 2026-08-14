package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	"github.com/gsoultan/panmail/internal/storetest"
)

// An API key is a credential. GetByHash is the lookup the auth middleware
// performs on every request, and the scopes it returns are what the RBAC
// interceptor then enforces — so a scope lost in the round trip silently
// changes what a key is allowed to do.

var fixedTime = time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)

func newRepo(t *testing.T) repositories.ApiKeyRepository {
	t.Helper()
	return NewApiKeyStore(storetest.NewConnection(t))
}

func apiKey(id, tenantID, hash string, scopes []entities.Scope) *entities.ApiKey {
	// The fixture is named for readability; the column is a UUID on PostgreSQL
	// and merely a VARCHAR on SQLite. Mapping here keeps call sites saying
	// "k1" while the database gets something it will accept — the difference
	// that let these fixtures pass for as long as only SQLite was run.
	id = storetest.ID(id)

	return &entities.ApiKey{
		ID:       id,
		TenantID: tenantID,
		Name:     "key-" + id,
		KeyHash:  hash,
		// Kept to the length production actually produces — plainKey[:7] — and
		// therefore within the column. Deriving it from the id overflowed once
		// the id became a UUID, which is a fixture problem rather than a sign
		// the column is too narrow.
		Prefix:    "pm_" + id[:4],
		Scopes:    scopes,
		IsEnabled: true,
		CreatedAt: fixedTime,
		UpdatedAt: fixedTime,
	}
}

func TestScopesSurviveTheRoundTrip(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	want := []entities.Scope{entities.ScopeEmailSend, entities.ScopeProvidersRead}
	if err := repo.Create(ctx, apiKey("k1", storetest.TenantA, "hash-1", want)); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("get by hash: %v", err)
	}
	if got == nil {
		t.Fatal("expected the key back")
	}
	if len(got.Scopes) != len(want) {
		t.Fatalf("scopes lost entries: got %v, want %v", got.Scopes, want)
	}
	for i := range want {
		if got.Scopes[i] != want[i] {
			t.Errorf("scope %d changed: got %q, want %q", i, got.Scopes[i], want[i])
		}
	}
	if got.TenantID != storetest.TenantA {
		t.Errorf("tenant did not round trip: %q", got.TenantID)
	}
}

// A key stored with no scopes normalises to the narrowest useful set rather
// than to nothing or to everything. Keys predating scopes have no stored value
// and land here, so what matters is that the fallback stays minimal: it must
// never include a management scope.
func TestAbsentScopesFallBackToTheNarrowDefault(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, apiKey("k1", storetest.TenantA, "hash-1", nil)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected the key back")
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != entities.ScopeEmailSend {
		t.Errorf("expected the default scope set, got %v", got.Scopes)
	}
	for _, s := range got.Scopes {
		if s == entities.ScopeProvidersWrite || s == entities.ScopeWebhooksWrite {
			t.Errorf("the default scope set must not include write authority, got %v", got.Scopes)
		}
	}
}

// Unknown scope strings must be dropped rather than carried through, or a
// hand-edited database row could name a scope the policy table does not know.
func TestUnknownScopesAreDropped(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	key := apiKey("k1", storetest.TenantA, "hash-1", []entities.Scope{entities.ScopeEmailSend, "not:a:scope"})
	if err := repo.Create(ctx, key); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for _, s := range got.Scopes {
		if s == "not:a:scope" {
			t.Errorf("an unknown scope survived storage: %v", got.Scopes)
		}
	}
}

// GetByHash is not tenant-scoped by design — the hash identifies the key and
// the tenant comes back with it — so the tenant it reports must be right, and
// every method that does take a tenant must honour it.
func TestKeysAreScopedToTheirTenant(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, apiKey("mine", storetest.TenantA, "hash-a", []entities.Scope{entities.ScopeEmailSend})); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := repo.Create(ctx, apiKey("theirs", storetest.TenantB, "hash-b", []entities.Scope{entities.ScopeEmailSend})); err != nil {
		t.Fatalf("create B: %v", err)
	}

	t.Run("GetByID cannot reach another tenant's key", func(t *testing.T) {
		got, err := repo.GetByID(ctx, storetest.ID("theirs"), storetest.TenantA)
		if err == nil && got != nil {
			t.Errorf("tenant A read tenant B's key: %+v", got)
		}
	})

	t.Run("ListByTenantID returns only the caller's keys", func(t *testing.T) {
		got, _, err := repo.ListByTenantID(ctx, storetest.TenantA, 50, "")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, k := range got {
			if k.TenantID != storetest.TenantA {
				t.Errorf("list leaked a key from %s", k.TenantID)
			}
		}
		if len(got) != 1 {
			t.Errorf("expected 1 key for tenant A, got %d", len(got))
		}
	})

	// Disabling is how a key is revoked. Reaching across tenants here would
	// let one tenant revoke another's credential.
	t.Run("UpdateStatus cannot disable another tenant's key", func(t *testing.T) {
		_ = repo.UpdateStatus(ctx, storetest.ID("theirs"), storetest.TenantA, false)

		victim, err := repo.GetByID(ctx, storetest.ID("theirs"), storetest.TenantB)
		if err != nil {
			t.Fatalf("get B: %v", err)
		}
		if victim == nil {
			t.Fatal("tenant B's key disappeared")
		}
		if !victim.IsEnabled {
			t.Error("tenant A disabled tenant B's key")
		}
	})

	t.Run("Delete cannot remove another tenant's key", func(t *testing.T) {
		_ = repo.Delete(ctx, storetest.ID("theirs"), storetest.TenantA)

		victim, err := repo.GetByID(ctx, storetest.ID("theirs"), storetest.TenantB)
		if err != nil {
			t.Fatalf("get B: %v", err)
		}
		if victim == nil {
			t.Error("tenant A deleted tenant B's key")
		}
	})
}

func TestUpdateStatusDisablesTheKey(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, apiKey("k1", storetest.TenantA, "hash-1", nil)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.UpdateStatus(ctx, storetest.ID("k1"), storetest.TenantA, false); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := repo.GetByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected the key back")
	}
	if got.IsEnabled {
		t.Error("key is still enabled after being disabled")
	}
}

// A revoked key must stop authenticating, so deletion has to actually remove
// the row the middleware looks up.
func TestDeletedKeyNoLongerResolvesByHash(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, apiKey("k1", storetest.TenantA, "hash-1", nil)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, storetest.ID("k1"), storetest.TenantA); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := repo.GetByHash(ctx, "hash-1")
	if err == nil && got != nil {
		t.Errorf("a deleted key still authenticates: %+v", got)
	}
}

// An unknown hash must report an error and never (nil, nil).
//
// This is deliberate and load-bearing, and differs from the other stores, which
// return (nil, nil) for a missing row. ApiKeyUsecase.Authenticate dereferences
// the result immediately — `if !apiKey.IsEnabled` — so a (nil, nil) here would
// panic on every request carrying an unrecognised key, which is exactly the
// request an attacker sends. Failing with an error keeps the auth path closed.
func TestUnknownHashErrorsRatherThanReturningNilNil(t *testing.T) {
	repo := newRepo(t)
	got, err := repo.GetByHash(context.Background(), "no-such-hash")
	if got != nil {
		t.Errorf("expected no key, got %+v", got)
	}
	if err == nil {
		t.Error("an unknown hash must return an error; callers dereference the result without a nil check")
	}
}

func TestUpdateLastUsedIsRecorded(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, apiKey("k1", storetest.TenantA, "hash-1", nil)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.UpdateLastUsed(ctx, storetest.ID("k1")); err != nil {
		t.Fatalf("update last used: %v", err)
	}

	got, err := repo.GetByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.LastUsedAt == nil {
		t.Error("last_used_at was not recorded")
	}
}

// An expiry has to survive storage, or an expired key keeps working.
func TestExpiryRoundTrips(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	expires := fixedTime.Add(24 * time.Hour)
	key := apiKey("k1", storetest.TenantA, "hash-1", nil)
	key.ExpiresAt = &expires
	if err := repo.Create(ctx, key); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.ExpiresAt == nil {
		t.Fatal("expiry was lost")
	}
	if !got.ExpiresAt.UTC().Equal(expires) {
		t.Errorf("expiry changed: got %s, want %s", got.ExpiresAt.UTC(), expires)
	}
}
