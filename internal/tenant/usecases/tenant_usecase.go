package usecases

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/tenant/entities"
	"github.com/gsoultan/panmail/internal/tenant/repositories"
)

type TenantUsecase interface {
	CreateTenant(ctx context.Context, name string, retryPattern []string, limits SendLimits) (*entities.Tenant, error)
	ListTenants(ctx context.Context, pageSize int, pageToken string) ([]*entities.Tenant, string, error)
	GetTenantByID(ctx context.Context, id string) (*entities.Tenant, error)
	UpdateTenant(ctx context.Context, id string, name string, retryPattern []string, limits SendLimits) (*entities.Tenant, error)
	DeleteTenant(ctx context.Context, id string) error
}

// SendLimits carries a tenant's send ceiling.
//
// A struct rather than two more positional parameters: they are both ints and
// adjacent, which is the signature most likely to be filled in the wrong order,
// and transposing a rate with a burst produces a plausible-looking limit that
// is wrong in a way nothing would catch.
type SendLimits struct {
	PerMinute int
	Burst     int
}

type tenantUsecase struct {
	repo repositories.TenantRepository
}

func NewTenantUsecase(repo repositories.TenantRepository) TenantUsecase {
	return &tenantUsecase{repo: repo}
}

func (u *tenantUsecase) CreateTenant(ctx context.Context, name string, retryPattern []string, limits SendLimits) (*entities.Tenant, error) {
	tenant := &entities.Tenant{
		ID:                uuid.New().String(),
		Name:              name,
		RetryPattern:      retryPattern,
		SendRatePerMinute: limits.PerMinute,
		SendBurst:         limits.Burst,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if err := u.repo.Create(ctx, tenant); err != nil {
		return nil, err
	}

	return tenant, nil
}

func (u *tenantUsecase) ListTenants(ctx context.Context, pageSize int, pageToken string) ([]*entities.Tenant, string, error) {
	return u.repo.List(ctx, pageSize, pageToken)
}

func (u *tenantUsecase) GetTenantByID(ctx context.Context, id string) (*entities.Tenant, error) {
	return u.repo.GetByID(ctx, id)
}

func (u *tenantUsecase) UpdateTenant(ctx context.Context, id string, name string, retryPattern []string, limits SendLimits) (*entities.Tenant, error) {
	tenant, err := u.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	tenant.Name = name
	tenant.RetryPattern = retryPattern
	tenant.SendRatePerMinute = limits.PerMinute
	tenant.SendBurst = limits.Burst
	tenant.UpdatedAt = time.Now()

	if err := u.repo.Update(ctx, tenant); err != nil {
		return nil, err
	}

	return tenant, nil
}

func (u *tenantUsecase) DeleteTenant(ctx context.Context, id string) error {
	return u.repo.Delete(ctx, id)
}
