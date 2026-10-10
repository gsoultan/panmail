package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// ErrTenantNotEmpty is returned when a tenant still owns data. The error
// carrying it is a *NotEmptyError, which says what remains.
var ErrTenantNotEmpty = errors.New("tenant is not empty")

// NotEmptyError lists what still belongs to a tenant that was asked to be
// deleted.
type NotEmptyError struct {
	Remaining []string
}

func (e *NotEmptyError) Error() string {
	return fmt.Sprintf("this tenant still has %s; remove them before deleting the tenant",
		strings.Join(e.Remaining, ", "))
}

func (e *NotEmptyError) Is(target error) bool { return target == ErrTenantNotEmpty }

// StateForgetter is in-memory, per-tenant state that should go when the tenant
// does — the send rate limiter's bucket, for one.
type StateForgetter interface {
	Forget(tenantID string)
}

type tenantUsecase struct {
	repo repositories.TenantRepository

	forgetters []StateForgetter
}

// NewTenantUsecase builds the usecase. Each forgetter is told when a tenant is
// deleted, so that nothing keeps state for a tenant that no longer exists.
func NewTenantUsecase(repo repositories.TenantRepository, forgetters ...StateForgetter) TenantUsecase {
	return &tenantUsecase{repo: repo, forgetters: forgetters}
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

// DeleteTenant removes an empty tenant, and refuses one that still owns data.
//
// The delete was a bare DELETE FROM tenants, and every table holding tenant
// data references tenants with a foreign key that SQLite enforces. Any tenant
// that had ever had a user, a provider or a sent message failed with a
// constraint error reported as Internal, which read as a bug and said nothing
// about what to do. Cascading instead was not an option to take quietly: the
// tenant's events, archives and inbound mail live outside SQL too, and nothing
// in the console says deleting a tenant destroys its mail. So the answer is a
// refusal that names what is left.
func (u *tenantUsecase) DeleteTenant(ctx context.Context, id string) error {
	if counter, ok := u.repo.(repositories.DependentCounter); ok {
		deps, err := counter.CountDependents(ctx, id)
		if err != nil {
			return err
		}
		if remaining := describeDependents(deps); len(remaining) > 0 {
			return &NotEmptyError{Remaining: remaining}
		}
	}

	if err := u.repo.Delete(ctx, id); err != nil {
		return err
	}
	for _, f := range u.forgetters {
		f.Forget(id)
	}
	return nil
}

func describeDependents(d repositories.Dependents) []string {
	var out []string
	for _, c := range []struct {
		n              int
		single, plural string
	}{
		{d.Users, "user", "users"},
		{d.GuestMembers, "guest member", "guest members"},
		{d.EmailProviders, "email provider", "email providers"},
		{d.ApiKeys, "API key", "API keys"},
		{d.Templates, "template", "templates"},
		{d.Suppressions, "suppression", "suppressions"},
		{d.Webhooks, "webhook", "webhooks"},
		{d.OutboxMessages, "queued message", "queued messages"},
	} {
		switch {
		case c.n == 1:
			out = append(out, "1 "+c.single)
		case c.n > 1:
			out = append(out, fmt.Sprintf("%d %s", c.n, c.plural))
		}
	}
	return out
}
