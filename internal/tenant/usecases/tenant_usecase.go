package usecases

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/tenant/entities"
	"github.com/gsoultan/panmail/internal/tenant/repositories"
)

type TenantUsecase interface {
	CreateTenant(ctx context.Context, name string, retryPattern []string) (*entities.Tenant, error)
	ListTenants(ctx context.Context, pageSize int, pageToken string) ([]*entities.Tenant, string, error)
	GetTenantByID(ctx context.Context, id string) (*entities.Tenant, error)
	UpdateTenant(ctx context.Context, id string, name string, retryPattern []string) (*entities.Tenant, error)
	DeleteTenant(ctx context.Context, id string) error
}

type tenantUsecase struct {
	repo repositories.TenantRepository
}

func NewTenantUsecase(repo repositories.TenantRepository) TenantUsecase {
	return &tenantUsecase{repo: repo}
}

func (u *tenantUsecase) CreateTenant(ctx context.Context, name string, retryPattern []string) (*entities.Tenant, error) {
	tenant := &entities.Tenant{
		ID:           uuid.New().String(),
		Name:         name,
		RetryPattern: retryPattern,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
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

func (u *tenantUsecase) UpdateTenant(ctx context.Context, id string, name string, retryPattern []string) (*entities.Tenant, error) {
	tenant, err := u.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	tenant.Name = name
	tenant.RetryPattern = retryPattern
	tenant.UpdatedAt = time.Now()

	if err := u.repo.Update(ctx, tenant); err != nil {
		return nil, err
	}

	return tenant, nil
}

func (u *tenantUsecase) DeleteTenant(ctx context.Context, id string) error {
	return u.repo.Delete(ctx, id)
}
