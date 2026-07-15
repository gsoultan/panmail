package usecases

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
)

type ApiKeyUsecase interface {
	CreateApiKey(ctx context.Context, tenantID, name string, expiresAt *time.Time) (*entities.ApiKey, string, error)
	ListApiKeys(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.ApiKey, string, error)
	DeleteApiKey(ctx context.Context, id, tenantID string) error
	DisableApiKey(ctx context.Context, id, tenantID string) error
	EnableApiKey(ctx context.Context, id, tenantID string) error
	VerifyApiKey(ctx context.Context, key string) (*entities.ApiKey, error)
}

type apiKeyUsecase struct {
	repo repositories.ApiKeyRepository
}

func NewApiKeyUsecase(repo repositories.ApiKeyRepository) ApiKeyUsecase {
	return &apiKeyUsecase{repo: repo}
}

func (u *apiKeyUsecase) CreateApiKey(ctx context.Context, tenantID, name string, expiresAt *time.Time) (*entities.ApiKey, string, error) {
	// Generate a random key
	randomBytes := make([]byte, 24)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, "", err
	}
	plainKey := fmt.Sprintf("pm_%s", hex.EncodeToString(randomBytes))

	// Hash the key
	hash := sha256.Sum256([]byte(plainKey))
	keyHash := hex.EncodeToString(hash[:])

	apiKey := &entities.ApiKey{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Name:      name,
		KeyHash:   keyHash,
		Prefix:    plainKey[:7], // pm_xxxx
		ExpiresAt: expiresAt,
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

	// Update last used at
	if err := u.repo.UpdateLastUsed(ctx, apiKey.ID); err != nil {
		slog.Error("failed to update api key last used at", "error", err, "id", apiKey.ID)
	}

	return apiKey, nil
}
