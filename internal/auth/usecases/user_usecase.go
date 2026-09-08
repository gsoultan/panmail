package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	"golang.org/x/crypto/bcrypt"
)

// ErrSuperAdminFromGuestTenant is returned when a caller acting inside a
// tenant that is not the target's home tries to change a super admin's role.
// The global role outranks any membership, so the change would look like it
// applied and then have no effect.
var ErrSuperAdminFromGuestTenant = errors.New("switch to the user's home tenant to change a super admin's role")

// UserUsecase manages the users of one tenant.
//
// Every method that names a user by id takes the tenant the caller is acting
// in and refuses ids outside it. Ids are not secrets — ListUsers hands them
// out, and since users can be members of several tenants the same id is now
// visible from more than one — so an id alone must not be enough to reach a
// user in another tenant.
type UserUsecase interface {
	CreateUser(ctx context.Context, tenantID, email, password, name, role string) (*entities.User, error)
	ListUsers(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.User, string, error)
	GetByID(ctx context.Context, id string) (*entities.User, error)
	// GetInTenant returns the user only if they are reachable from the given
	// tenant, either as its native or as a guest member.
	GetInTenant(ctx context.Context, tenantID, id string) (*entities.User, error)
	UpdateUserRole(ctx context.Context, tenantID, id, role string) error
	UpdateUserTwoFactor(ctx context.Context, tenantID, id string, enabled bool) error
	DeleteUser(ctx context.Context, tenantID, id string) error
}

type userUsecase struct {
	repo        repositories.UserRepository
	memberships repositories.MembershipRepository
}

func NewUserUsecase(repo repositories.UserRepository, memberships repositories.MembershipRepository) UserUsecase {
	return &userUsecase{repo: repo, memberships: memberships}
}

func (u *userUsecase) CreateUser(ctx context.Context, tenantID, email, password, name, role string) (*entities.User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	user := &entities.User{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Email:     email,
		Password:  string(hashedPassword),
		Name:      name,
		Role:      role,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := u.repo.Create(ctx, user); err != nil {
		return nil, err
	}

	// The home tenant is a membership like any other, so that "which tenants
	// can this user reach" is one query. Without this row a freshly created
	// user would be missing from their own tenant's member list.
	if err := u.assignHomeMembership(ctx, user); err != nil {
		return nil, err
	}

	return user, nil
}

// assignHomeMembership records the membership implied by users.tenant_id. A
// super admin's global role is not a tenant role, so their membership row
// carries administrator — the highest role that can be held locally — while
// the global role continues to outrank it.
func (u *userUsecase) assignHomeMembership(ctx context.Context, user *entities.User) error {
	if user.TenantID == "" || u.memberships == nil {
		return nil
	}
	role := user.Role
	if !entities.IsTenantRole(role) {
		role = entities.RoleAdmin
	}
	return u.memberships.Assign(ctx, &entities.UserTenant{
		UserID:    user.ID,
		TenantID:  user.TenantID,
		Role:      role,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	})
}

func (u *userUsecase) ListUsers(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.User, string, error) {
	return u.repo.ListByTenantID(ctx, tenantID, pageSize, pageToken)
}

func (u *userUsecase) GetByID(ctx context.Context, id string) (*entities.User, error) {
	return u.repo.GetByID(ctx, id)
}

func (u *userUsecase) GetInTenant(ctx context.Context, tenantID, id string) (*entities.User, error) {
	user, err := u.repo.GetByID(ctx, id)
	if err != nil || user == nil {
		return nil, ErrUserNotFound
	}
	if user.TenantID == tenantID {
		return user, nil
	}
	if u.memberships != nil {
		membership, err := u.memberships.Get(ctx, id, tenantID)
		if err != nil {
			return nil, err
		}
		if membership != nil {
			// Inside this tenant the user holds their membership role, not
			// their home role. Report the one that applies here.
			if user.Role != entities.RoleSuperAdmin {
				user.Role = membership.Role
			}
			return user, nil
		}
	}
	return nil, ErrUserNotFound
}

// UpdateUserRole changes what the user may do. Which record it writes depends
// on where the caller is acting:
//
//   - super admin, in either direction, is a global capability and is written
//     to users.role;
//   - inside the user's home tenant it writes users.role, because that is what
//     sign-in reads, and keeps the home membership row in step;
//   - inside any other tenant it writes only that membership, so a guest's
//     role elsewhere is untouched.
func (u *userUsecase) UpdateUserRole(ctx context.Context, tenantID, id, role string) error {
	user, err := u.GetInTenant(ctx, tenantID, id)
	if err != nil {
		return err
	}

	isHome := user.TenantID == tenantID
	targetIsSuperAdmin := user.Role == entities.RoleSuperAdmin

	if role == entities.RoleSuperAdmin {
		return u.repo.UpdateRole(ctx, id, role)
	}

	// Demoting a super admin from a tenant they are only a guest in would
	// write a membership row that their global role overrules, so the caller
	// would see no change. Refuse instead of pretending.
	if targetIsSuperAdmin && !isHome {
		return ErrSuperAdminFromGuestTenant
	}

	if isHome {
		if err := u.repo.UpdateRole(ctx, id, role); err != nil {
			return err
		}
		if u.memberships == nil {
			return nil
		}
		return u.memberships.UpdateRole(ctx, id, tenantID, role)
	}

	if u.memberships == nil {
		return ErrNotAMember
	}
	return u.memberships.UpdateRole(ctx, id, tenantID, role)
}

func (u *userUsecase) UpdateUserTwoFactor(ctx context.Context, tenantID, id string, enabled bool) error {
	user, err := u.GetInTenant(ctx, tenantID, id)
	if err != nil {
		return err
	}
	return u.repo.UpdateTwoFactor(ctx, id, enabled, user.TwoFactorSecret)
}

// DeleteUser removes the account itself, so it is refused from any tenant the
// user is only a guest in: an administrator of a tenant somebody visits must
// not be able to delete that person's account. Revoking a guest is
// RemoveUserFromTenant.
func (u *userUsecase) DeleteUser(ctx context.Context, tenantID, id string) error {
	user, err := u.GetInTenant(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if user.TenantID != tenantID {
		return ErrHomeTenantOnlyDeletion
	}

	// Memberships go first: the row in users is what the foreign key points
	// at, and SQLite only enforces cascades when the connection enables them.
	if u.memberships != nil {
		if err := u.memberships.RemoveAllForUser(ctx, id); err != nil {
			return err
		}
	}
	return u.repo.Delete(ctx, id)
}

// ErrHomeTenantOnlyDeletion is returned when a caller tries to delete a user
// who is only visiting their tenant.
var ErrHomeTenantOnlyDeletion = errors.New("this user's account belongs to another tenant; remove their membership instead")
