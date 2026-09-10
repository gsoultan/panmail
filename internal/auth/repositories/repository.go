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

// MembershipRepository stores which tenants a user may act in.
//
// A user's home tenant (users.tenant_id) has a row here too, so ListByUser
// returns the complete set without the caller having to union in the home
// tenant separately.
type MembershipRepository interface {
	// Assign creates the membership, or updates the role if it already exists.
	Assign(ctx context.Context, m *entities.UserTenant) error
	Remove(ctx context.Context, userID, tenantID string) error
	// Get returns (nil, nil) when the user is not a member of the tenant, so
	// that "not a member" is an answer rather than an error to classify.
	Get(ctx context.Context, userID, tenantID string) (*entities.UserTenant, error)
	ListByUser(ctx context.Context, userID string) ([]*entities.UserTenant, error)
	UpdateRole(ctx context.Context, userID, tenantID, role string) error
	// RemoveAllForUser clears every membership, for use when the user is
	// deleted. It is explicit rather than left to ON DELETE CASCADE because
	// SQLite only enforces foreign keys when the connection asks it to.
	RemoveAllForUser(ctx context.Context, userID string) error
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
