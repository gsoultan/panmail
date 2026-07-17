package stores

import (
	"context"

	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
)

type Repository interface {
	Create(ctx context.Context, provider *entities.EmailProvider) error
	GetByID(ctx context.Context, tenantID, id string) (*entities.EmailProvider, error)
	List(ctx context.Context, tenantID string, name string, providerType string, pageSize int, pageToken string) ([]*entities.EmailProvider, string, error)
	Update(ctx context.Context, provider *entities.EmailProvider) error
	Delete(ctx context.Context, tenantID, id string) error
}
