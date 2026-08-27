package usecases

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	"github.com/gsoultan/panmail/pkg/cache"
)

// lastUsedResolution is how stale the "last used" timestamp is allowed to be.
// Writing it on every request turns each API call into an extra database write
// against a single row; once a minute is enough to answer "is this key still
// in use?".
const lastUsedResolution = time.Minute

// NewApiKey describes a key to mint.
type NewApiKey struct {
	TenantID  string
	Name      string
	Scopes    []entities.Scope
	ExpiresAt *time.Time
}

type ApiKeyUsecase interface {
	CreateApiKey(ctx context.Context, req NewApiKey) (*entities.ApiKey, string, error)
	ListApiKeys(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.ApiKey, string, error)
	DeleteApiKey(ctx context.Context, id, tenantID string) error
	DisableApiKey(ctx context.Context, id, tenantID string) error
	EnableApiKey(ctx context.Context, id, tenantID string) error
	VerifyApiKey(ctx context.Context, key string) (*entities.ApiKey, error)
}

// Bounds on the verified-key cache.
//
// Every authenticated request verified its key against the database, which
// made this the most-executed query in the process: at a few thousand requests
// a second it is that many indexed lookups, each holding a pooled connection
// to ask whether a key the process saw milliseconds ago is still valid.
//
// The TTL is short and deliberately so — it is the window in which a key
// revoked on *another* instance still works here. Revocation through this
// instance evicts immediately, so the window only exists across instances.
const (
	apiKeyCacheTTL = 5 * time.Second

	// The cache is keyed by the hash of whatever key a caller presented, which
	// is attacker-supplied. Only verified keys are ever stored, so the entry
	// count is bounded by how many real keys exist rather than by how many
	// strings someone can send — and the limit bounds it again regardless.
	apiKeyCacheMaxEntries = 4096
)

type apiKeyUsecase struct {
	repo repositories.ApiKeyRepository

	lastUsedMu    sync.Mutex
	lastUsedFlush map[string]time.Time

	// verified holds keys that passed every check, by key hash.
	verified *cache.TTLCache[*entities.ApiKey]
}

func NewApiKeyUsecase(repo repositories.ApiKeyRepository) ApiKeyUsecase {
	return &apiKeyUsecase{
		repo:          repo,
		lastUsedFlush: make(map[string]time.Time),
		verified: cache.NewWithLimit[*entities.ApiKey](
			apiKeyCacheTTL, apiKeyCacheMaxEntries,
		),
	}
}

func (u *apiKeyUsecase) CreateApiKey(ctx context.Context, req NewApiKey) (*entities.ApiKey, string, error) {
	randomBytes := make([]byte, 24)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, "", err
	}
	plainKey := fmt.Sprintf("pm_%s", hex.EncodeToString(randomBytes))

	hash := sha256.Sum256([]byte(plainKey))
	keyHash := hex.EncodeToString(hash[:])

	apiKey := &entities.ApiKey{
		ID:        uuid.New().String(),
		TenantID:  req.TenantID,
		Name:      req.Name,
		KeyHash:   keyHash,
		Prefix:    plainKey[:7], // pm_xxxx
		Scopes:    entities.NormalizeScopes(req.Scopes),
		ExpiresAt: req.ExpiresAt,
		IsEnabled: true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := u.repo.Create(ctx, apiKey); err != nil {
		return nil, "", err
	}

	return apiKey, plainKey, nil
}

func (u *apiKeyUsecase) ListApiKeys(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.ApiKey, string, error) {
	return u.repo.ListByTenantID(ctx, tenantID, pageSize, pageToken)
}

func (u *apiKeyUsecase) DeleteApiKey(ctx context.Context, id, tenantID string) error {
	// Read before deleting: afterwards there is no row to learn the hash from,
	// and the hash is how the cached entry is found.
	hash := u.hashOf(ctx, id, tenantID)
	if err := u.repo.Delete(ctx, id, tenantID); err != nil {
		return err
	}
	u.forget(hash)
	return nil
}

func (u *apiKeyUsecase) DisableApiKey(ctx context.Context, id, tenantID string) error {
	if err := u.repo.UpdateStatus(ctx, id, tenantID, false); err != nil {
		return err
	}
	u.forget(u.hashOf(ctx, id, tenantID))
	return nil
}

func (u *apiKeyUsecase) EnableApiKey(ctx context.Context, id, tenantID string) error {
	if err := u.repo.UpdateStatus(ctx, id, tenantID, true); err != nil {
		return err
	}
	// Enabling evicts too. A key disabled and re-enabled inside one TTL would
	// otherwise keep serving whichever state happened to be cached.
	u.forget(u.hashOf(ctx, id, tenantID))
	return nil
}

// hashOf finds the cache key for an API key id, or "" when it cannot.
func (u *apiKeyUsecase) hashOf(ctx context.Context, id, tenantID string) string {
	key, err := u.repo.GetByID(ctx, id, tenantID)
	if err != nil || key == nil {
		return ""
	}
	return key.KeyHash
}

// forget drops a key from the verified cache, so a revocation made through
// this instance takes effect on the next request rather than after the TTL.
func (u *apiKeyUsecase) forget(keyHash string) {
	if keyHash == "" {
		// The hash could not be read, so the entry cannot be found. It still
		// expires with the TTL; say so, because the alternative is a
		// revocation that looks instant and is not.
		slog.Warn("could not evict a cached api key; it stays valid until the cache entry expires",
			"ttl", apiKeyCacheTTL)
		return
	}
	u.verified.Delete(keyHash)
}

func (u *apiKeyUsecase) VerifyApiKey(ctx context.Context, key string) (*entities.ApiKey, error) {
	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])

	if cached, ok := u.verified.Get(keyHash); ok {
		// Expiry is re-checked on every hit rather than trusted from the
		// moment it was cached: a key whose expiry falls inside the TTL window
		// would otherwise keep working past it.
		if cached.ExpiresAt != nil && !cached.ExpiresAt.After(time.Now()) {
			u.verified.Delete(keyHash)
		} else {
			u.touchLastUsed(ctx, cached.ID)
			return cached, nil
		}
	}

	apiKey, err := u.repo.GetByHash(ctx, keyHash)
	if err != nil {
		return nil, err
	}
	// The store reports an unknown hash as an error, so this is unreachable
	// today. It is here because the dereference below is on the path every
	// unauthenticated request takes: were the store ever changed to match the
	// other repositories, which return (nil, nil) for a missing row, an
	// unrecognised key would panic instead of being rejected.
	if apiKey == nil {
		return nil, fmt.Errorf("api key not found")
	}

	if !apiKey.IsEnabled {
		return nil, fmt.Errorf("api key is disabled")
	}

	if apiKey.ExpiresAt != nil && apiKey.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("api key has expired")
	}

	// Only a key that passed every check above is cached. Caching the failures
	// would let anyone fill this map by presenting keys that do not exist, and
	// evict the real entries while they were at it.
	u.verified.Put(keyHash, apiKey)

	u.touchLastUsed(ctx, apiKey.ID)

	return apiKey, nil
}

// touchLastUsed records usage at most once per lastUsedResolution per key.
func (u *apiKeyUsecase) touchLastUsed(ctx context.Context, id string) {
	if !u.shouldFlushLastUsed(id) {
		return
	}
	if err := u.repo.UpdateLastUsed(ctx, id); err != nil {
		slog.Error("failed to update api key last used at", "error", err, "id", id)
	}
}

func (u *apiKeyUsecase) shouldFlushLastUsed(id string) bool {
	now := time.Now()

	u.lastUsedMu.Lock()
	defer u.lastUsedMu.Unlock()

	if last, ok := u.lastUsedFlush[id]; ok && now.Sub(last) < lastUsedResolution {
		return false
	}
	u.pruneLastUsedLocked(now)
	u.lastUsedFlush[id] = now
	return true
}

// pruneLastUsedLocked keeps the bookkeeping map from growing without bound as
// keys are rotated. The caller must hold the lock.
func (u *apiKeyUsecase) pruneLastUsedLocked(now time.Time) {
	for id, last := range u.lastUsedFlush {
		if now.Sub(last) > lastUsedResolution {
			delete(u.lastUsedFlush, id)
		}
	}
}
