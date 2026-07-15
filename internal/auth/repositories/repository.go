package repositories

import (
	"context"
	"github.com/gsoultan/panmail/internal/auth/entities"
)

type UserRepository interface {
	Create(ctx context.Context, u *entities.User) error
	GetByEmail(ctx context.Context, email string) (*entities.User, error)
	GetByID(ctx context.Context, id string) (*entities.User, error)
	ListByTenantID(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.User, string, error)
	UpdateRole(ctx context.Context, id string, role string) error
	UpdateTwoFactor(ctx context.Context, id string, enabled bool, secret string) error
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int, error)
}

type ApiKeyRepository interface {
	Create(ctx context.Context, key *entities.ApiKey) error
	ListByTenantID(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.ApiKey, string, error)
	Delete(ctx context.Context, id string, tenantID string) error
	GetByHash(ctx context.Context, hash string) (*entities.ApiKey, error)
	GetByID(ctx context.Context, id string, tenantID string) (*entities.ApiKey, error)
	UpdateStatus(ctx context.Context, id string, tenantID string, isEnabled bool) error
	UpdateLastUsed(ctx context.Context, id string) error
}
