package usecases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
)

// The verified-key cache removes a database round trip from every
// authenticated request. What it must not remove is the ability to take a key
// away: these cover the paths where a cached key has to stop working.

const cacheTestKey = "pm_live_cache_key"

// cacheTestRepo serves one key and lets a test revoke or disable it, counting
// the lookups so a test can tell a cache hit from a miss.
type cacheTestRepo struct {
	benchAPIKeyRepo
	deleted  bool
	disabled bool
}

func (r *cacheTestRepo) GetByHash(ctx context.Context, hash string) (*entities.ApiKey, error) {
	r.lookups.Add(1)
	if r.deleted {
		return nil, errKeyNotFound
	}
	key := *r.key
	key.IsEnabled = !r.disabled
	return &key, nil
}

func (r *cacheTestRepo) GetByID(context.Context, string, string) (*entities.ApiKey, error) {
	return r.key, nil
}

func (r *cacheTestRepo) Delete(context.Context, string, string) error {
	r.deleted = true
	return nil
}

func (r *cacheTestRepo) UpdateStatus(_ context.Context, _, _ string, enabled bool) error {
	r.disabled = !enabled
	return nil
}

var errKeyNotFound = &notFoundError{}

type notFoundError struct{}

func (*notFoundError) Error() string { return "api key not found" }

func newCacheTestRepo() *cacheTestRepo {
	// KeyHash has to be the real hash of cacheTestKey. Eviction finds the
	// cached entry by the hash stored on the row, so a fixture that invented
	// one would evict nothing and quietly pass a test that proves the
	// opposite of what it claims.
	sum := sha256.Sum256([]byte(cacheTestKey))

	return &cacheTestRepo{
		benchAPIKeyRepo: benchAPIKeyRepo{
			key: &entities.ApiKey{
				ID: "key-1", TenantID: "tenant-1",
				KeyHash:   hex.EncodeToString(sum[:]),
				IsEnabled: true,
				Scopes:    []entities.Scope{entities.ScopeEmailSend},
			},
		},
	}
}

func TestVerifyApiKeyServesRepeatedRequestsWithoutTheDatabase(t *testing.T) {
	repo := newCacheTestRepo()
	usecase := NewApiKeyUsecase(repo)
	ctx := context.Background()

	for range 50 {
		if _, err := usecase.VerifyApiKey(ctx, cacheTestKey); err != nil {
			t.Fatalf("VerifyApiKey() error = %v", err)
		}
	}

	if got := repo.lookups.Load(); got != 1 {
		t.Errorf("50 requests cost %d database lookups, want 1", got)
	}
}

// Revoking through this instance must take effect on the next request, not
// after the TTL. That is the whole reason eviction exists.
func TestARevokedKeyStopsWorkingImmediately(t *testing.T) {
	testCases := []struct {
		name   string
		revoke func(ApiKeyUsecase, context.Context) error
	}{
		{
			name: "deleted",
			revoke: func(u ApiKeyUsecase, ctx context.Context) error {
				return u.DeleteApiKey(ctx, "key-1", "tenant-1")
			},
		},
		{
			name: "disabled",
			revoke: func(u ApiKeyUsecase, ctx context.Context) error {
				return u.DisableApiKey(ctx, "key-1", "tenant-1")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newCacheTestRepo()
			usecase := NewApiKeyUsecase(repo)
			ctx := context.Background()

			if _, err := usecase.VerifyApiKey(ctx, cacheTestKey); err != nil {
				t.Fatalf("VerifyApiKey() error = %v", err)
			}

			if err := tc.revoke(usecase, ctx); err != nil {
				t.Fatalf("revoke: %v", err)
			}

			// No sleep: the point is that this does not wait for the TTL.
			if _, err := usecase.VerifyApiKey(ctx, cacheTestKey); err == nil {
				t.Fatal("a revoked key was still accepted")
			}
		})
	}
}

// A key can expire while it sits in the cache, so expiry is re-checked on
// every hit rather than trusted from the moment it was stored.
func TestAKeyThatExpiresWhileCachedIsRejected(t *testing.T) {
	repo := newCacheTestRepo()
	expiry := time.Now().Add(40 * time.Millisecond)
	repo.key.ExpiresAt = &expiry

	usecase := NewApiKeyUsecase(repo)
	ctx := context.Background()

	if _, err := usecase.VerifyApiKey(ctx, cacheTestKey); err != nil {
		t.Fatalf("VerifyApiKey() error = %v", err)
	}

	// Still inside the cache TTL, but past the key's own expiry.
	time.Sleep(80 * time.Millisecond)

	if _, err := usecase.VerifyApiKey(ctx, cacheTestKey); err == nil {
		t.Fatal("an expired key was served from the cache")
	}
}

// The cache is keyed by the hash of whatever a caller presented, which is
// attacker-supplied. Caching the failures would let anyone fill it with keys
// that do not exist and evict the real entries while they were at it.
func TestAnUnknownKeyIsNeverCached(t *testing.T) {
	repo := newCacheTestRepo()
	repo.deleted = true // every lookup fails
	usecase := NewApiKeyUsecase(repo)
	ctx := context.Background()

	const attempts = 20
	for range attempts {
		if _, err := usecase.VerifyApiKey(ctx, "pm_live_not_a_real_key"); err == nil {
			t.Fatal("an unknown key was accepted")
		}
	}

	// Every attempt must reach the database. If failures were cached, the
	// count would stop at one and the map would be attacker-fillable.
	if got := repo.lookups.Load(); got != attempts {
		t.Errorf("%d rejected attempts cost %d lookups, want %d", attempts, got, attempts)
	}
}

// A disabled key is refused even before eviction gets a chance, because the
// enabled check runs on the value that was cached.
func TestADisabledKeyIsNotServedFromTheCache(t *testing.T) {
	repo := newCacheTestRepo()
	usecase := NewApiKeyUsecase(repo)
	ctx := context.Background()

	repo.disabled = true
	if _, err := usecase.VerifyApiKey(ctx, cacheTestKey); err == nil {
		t.Fatal("a disabled key was accepted")
	}

	// And it was not cached on the way through.
	if _, err := usecase.VerifyApiKey(ctx, cacheTestKey); err == nil {
		t.Fatal("a disabled key was accepted on a second attempt")
	}
	if got := repo.lookups.Load(); got != 2 {
		t.Errorf("two rejected attempts cost %d lookups, want 2", got)
	}
}
