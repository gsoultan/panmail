package repositories

import (
	"context"

	"github.com/gsoultan/panmail/internal/tenant/entities"
)

type TenantRepository interface {
	Create(ctx context.Context, tenant *entities.Tenant) error
	GetByID(ctx context.Context, id string) (*entities.Tenant, error)
	List(ctx context.Context, pageSize int, pageToken string) ([]*entities.Tenant, string, error)
	Update(ctx context.Context, tenant *entities.Tenant) error
	Delete(ctx context.Context, id string) error
}
