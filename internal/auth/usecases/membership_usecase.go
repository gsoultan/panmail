package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	tenantrepositories "github.com/gsoultan/panmail/internal/tenant/repositories"
)

// Errors a caller is expected to distinguish. The service layer maps these to
// status codes, so they are values rather than formatted strings.
var (
	ErrUserNotFound      = errors.New("user not found")
	ErrTenantNotFound    = errors.New("tenant not found")
	ErrNotAMember        = errors.New("user is not a member of that tenant")
	ErrHomeTenantRemoval = errors.New("cannot remove a user from their home tenant")
	ErrRoleNotAssignable = errors.New("that role cannot be granted as a tenant membership")
)

// MembershipUsecase assigns existing users to additional tenants.
//
// The alternative it replaces was creating a second account with the same
// address, which users.email UNIQUE refuses, so the same person ended up with
// an aliased address and a separate password per tenant. One account, many
// memberships, one password.
type MembershipUsecase interface {
	// AssignUserToTenant grants an existing user access to a tenant with the
	// given role, or changes that role if they are already a member.
	AssignUserToTenant(ctx context.Context, userID, tenantID, role string) (*entities.UserTenant, error)

	// RemoveUserFromTenant revokes a guest membership. It refuses the user's
	// home tenant.
	RemoveUserFromTenant(ctx context.Context, userID, tenantID string) error

	// ListUserTenants returns every tenant the user may act in.
	ListUserTenants(ctx context.Context, userID string) ([]*entities.UserTenant, error)

	// RoleIn returns the role the user holds in a tenant, or "" when they hold
	// none. It is the single answer to "may this caller act here", used by the
	// auth middleware when a request names a tenant other than the token's.
	RoleIn(ctx context.Context, userID, tenantID string) (string, error)
}

type membershipUsecase struct {
	memberships repositories.MembershipRepository
	users       repositories.UserRepository
	tenants     tenantrepositories.TenantRepository
}

func NewMembershipUsecase(
	memberships repositories.MembershipRepository,
	users repositories.UserRepository,
	tenants tenantrepositories.TenantRepository,
) MembershipUsecase {
	return &membershipUsecase{memberships: memberships, users: users, tenants: tenants}
}

func (u *membershipUsecase) AssignUserToTenant(ctx context.Context, userID, tenantID, role string) (*entities.UserTenant, error) {
	if !entities.IsTenantRole(role) {
		return nil, fmt.Errorf("%w: %q", ErrRoleNotAssignable, role)
	}

	user, err := u.users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, ErrUserNotFound
	}

	// The tenant must exist before the row is written. The foreign key would
	// catch it too, but a violation surfaces as an opaque driver error and the
	// caller cannot tell it apart from a genuine failure.
	tenant, err := u.tenants.GetByID(ctx, tenantID)
	if err != nil || tenant == nil {
		return nil, ErrTenantNotFound
	}

	now := time.Now()
	membership := &entities.UserTenant{
		UserID:     userID,
		TenantID:   tenantID,
		TenantName: tenant.Name,
		Role:       role,
		IsHome:     tenantID == user.TenantID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := u.memberships.Assign(ctx, membership); err != nil {
		return nil, err
	}

	// Assigning the home tenant is a role change there, and users.role is what
	// sign-in reads for the home tenant. Writing only the membership row would
	// leave the two disagreeing, and the token would keep the old role. A
	// super admin's global role is left alone: it outranks any membership and
	// is not this call's to lower.
	if membership.IsHome && user.Role != entities.RoleSuperAdmin {
		if err := u.users.UpdateRole(ctx, userID, role); err != nil {
			return nil, err
		}
	}

	return membership, nil
}

func (u *membershipUsecase) RemoveUserFromTenant(ctx context.Context, userID, tenantID string) error {
	user, err := u.users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return ErrUserNotFound
	}

	// Home membership is what sign-in lands on. Deleting it would leave an
	// account that authenticates into a tenant it is no longer recorded as
	// belonging to, which is worse than refusing. Deleting the user, or moving
	// their home tenant, are the operations that mean this.
	if tenantID == user.TenantID {
		return ErrHomeTenantRemoval
	}

	existing, err := u.memberships.Get(ctx, userID, tenantID)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrNotAMember
	}

	return u.memberships.Remove(ctx, userID, tenantID)
}

func (u *membershipUsecase) ListUserTenants(ctx context.Context, userID string) ([]*entities.UserTenant, error) {
	user, err := u.users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, ErrUserNotFound
	}

	memberships, err := u.memberships.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, m := range memberships {
		m.IsHome = m.TenantID == user.TenantID
		// The home role is users.role — see AssignUserToTenant — and a super
		// admin holds their global role everywhere. Report what the caller
		// would actually get, not the stored membership row.
		if user.Role == entities.RoleSuperAdmin {
			m.Role = entities.RoleSuperAdmin
		} else if m.IsHome {
			m.Role = user.Role
		}
	}
	return memberships, nil
}

func (u *membershipUsecase) RoleIn(ctx context.Context, userID, tenantID string) (string, error) {
	user, err := u.users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return "", ErrUserNotFound
	}

	// Super admin is a global capability: it is held on the user and reaches
	// every tenant, which is what makes support and monitoring possible
	// without an explicit membership in each one.
	if user.Role == entities.RoleSuperAdmin {
		return entities.RoleSuperAdmin, nil
	}

	if tenantID == user.TenantID {
		return user.Role, nil
	}

	membership, err := u.memberships.Get(ctx, userID, tenantID)
	if err != nil {
		return "", err
	}
	if membership == nil {
		return "", nil
	}
	return membership.Role, nil
}
