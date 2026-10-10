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

// Dependents counts, per table, the rows that still belong to a tenant and
// would make deleting it fail on a foreign key.
type Dependents struct {
	Users          int
	GuestMembers   int
	EmailProviders int
	ApiKeys        int
	Templates      int
	Suppressions   int
	Webhooks       int
	OutboxMessages int
}

// DependentCounter reports what still belongs to a tenant. It is a separate
// interface from TenantRepository so the many test doubles of that one do not
// all have to grow a method only tenant deletion uses.
type DependentCounter interface {
	CountDependents(ctx context.Context, id string) (Dependents, error)
}
