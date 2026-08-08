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

type apiKeyUsecase struct {
	repo repositories.ApiKeyRepository

	lastUsedMu    sync.Mutex
	lastUsedFlush map[string]time.Time
}

func NewApiKeyUsecase(repo repositories.ApiKeyRepository) ApiKeyUsecase {
	return &apiKeyUsecase{
		repo:          repo,
		lastUsedFlush: make(map[string]time.Time),
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
	return u.repo.Delete(ctx, id, tenantID)
}

func (u *apiKeyUsecase) DisableApiKey(ctx context.Context, id, tenantID string) error {
	return u.repo.UpdateStatus(ctx, id, tenantID, false)
}

func (u *apiKeyUsecase) EnableApiKey(ctx context.Context, id, tenantID string) error {
	return u.repo.UpdateStatus(ctx, id, tenantID, true)
}

func (u *apiKeyUsecase) VerifyApiKey(ctx context.Context, key string) (*entities.ApiKey, error) {
	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])

	apiKey, err := u.repo.GetByHash(ctx, keyHash)
	if err != nil {
		return nil, err
	}

	if !apiKey.IsEnabled {
		return nil, fmt.Errorf("api key is disabled")
	}

	if apiKey.ExpiresAt != nil && apiKey.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("api key has expired")
	}

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
