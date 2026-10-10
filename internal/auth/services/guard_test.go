package services

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/usecases"
)

// Changes an administrator makes to another account. Every one of them is a
// write keyed on a user id, and ids are not secrets, so each has to confirm the
// target is in the caller's tenant and is not a super admin they outrank.

// tenantUsers answers GetInTenant from a table and records which mutations
// reached it, so a refusal can be told from a call that did nothing.
type tenantUsers struct {
	usecases.UserUsecase // unused methods panic, which is what a test wants

	users map[string]*entities.User

	roleChanged []string
	deleted     []string
	twoFactor   []string
}

func newTenantUsers(users ...*entities.User) *tenantUsers {
	t := &tenantUsers{users: map[string]*entities.User{}}
	for _, u := range users {
		t.users[u.ID] = u
	}
	return t
}

func (t *tenantUsers) GetInTenant(ctx context.Context, tenantID, id string) (*entities.User, error) {
	u, ok := t.users[id]
	if !ok || u.TenantID != tenantID {
		return nil, usecases.ErrUserNotFound
	}
	copied := *u
	return &copied, nil
}

func (t *tenantUsers) UpdateUserRole(ctx context.Context, tenantID, id, role string) error {
	t.roleChanged = append(t.roleChanged, id)
	return nil
}

func (t *tenantUsers) DeleteUser(ctx context.Context, tenantID, id string) error {
	t.deleted = append(t.deleted, id)
	return nil
}

func (t *tenantUsers) UpdateUserTwoFactor(ctx context.Context, tenantID, id string, enabled bool) error {
	t.twoFactor = append(t.twoFactor, id)
	return nil
}

// disablingAuth records DisableTwoFactor calls.
type disablingAuth struct {
	usecases.AuthUsecase
	disabled []string
}

func (a *disablingAuth) DisableTwoFactor(ctx context.Context, userID string) error {
	a.disabled = append(a.disabled, userID)
	return nil
}

// The users every case below is built from. "t1" is the caller's tenant; the
// super admin lives there too, which is what put them within reach.
func guardFixtures() *tenantUsers {
	return newTenantUsers(
		&entities.User{ID: "member", TenantID: "t1", Role: entities.RoleEditor},
		&entities.User{ID: "root", TenantID: "t1", Role: entities.RoleSuperAdmin},
		&entities.User{ID: "elsewhere", TenantID: "t2", Role: entities.RoleViewer},
	)
}

type guardCase struct {
	name     string
	caller   string // role
	target   string
	wantCode connect.Code // 0 means allowed
}

var guardCases = []guardCase{
	{"admin on a member of their tenant", entities.RoleAdmin, "member", 0},
	{"admin on a super admin homed in their tenant", entities.RoleAdmin, "root", connect.CodePermissionDenied},
	{"admin on a user of another tenant", entities.RoleAdmin, "elsewhere", connect.CodeNotFound},
	{"admin on an id that does not exist", entities.RoleAdmin, "nobody", connect.CodeNotFound},
	{"super admin on a super admin", entities.RoleSuperAdmin, "root", 0},
}

func checkGuard(t *testing.T, tc guardCase, err error, reached []string) {
	t.Helper()
	if tc.wantCode == 0 {
		if err != nil {
			t.Fatalf("refused: %v", err)
		}
		if len(reached) != 1 || reached[0] != tc.target {
			t.Fatalf("the change reached %v, want [%s]", reached, tc.target)
		}
		return
	}
	if got := connect.CodeOf(err); got != tc.wantCode {
		t.Fatalf("code = %v (err %v), want %v", got, err, tc.wantCode)
	}
	if len(reached) != 0 {
		t.Fatalf("refused, yet the change reached %v", reached)
	}
}

func TestDisableTwoFactorForSomebodyElseIsConfinedToTheTenant(t *testing.T) {
	for _, tc := range guardCases {
		t.Run(tc.name, func(t *testing.T) {
			auth := &disablingAuth{}
			svc := NewAuthService(auth, nil, guardFixtures())

			_, err := svc.DisableTwoFactor(callerContext("caller", tc.caller),
				connect.NewRequest(&panmailv1.DisableTwoFactorRequest{UserId: tc.target}))
			checkGuard(t, tc, err, auth.disabled)
		})
	}
}

// Turning off your own second factor needs no standing over anybody.
func TestDisableTwoFactorForYourselfNeedsNoLookup(t *testing.T) {
	auth := &disablingAuth{}
	svc := NewAuthService(auth, nil, nil)

	if _, err := svc.DisableTwoFactor(callerContext("me", entities.RoleViewer),
		connect.NewRequest(&panmailv1.DisableTwoFactorRequest{})); err != nil {
		t.Fatalf("disable own 2FA: %v", err)
	}
	if len(auth.disabled) != 1 || auth.disabled[0] != "me" {
		t.Errorf("disabled %v, want [me]", auth.disabled)
	}
}

func TestUpdateUserTwoFactorGuardsSuperAdmins(t *testing.T) {
	for _, tc := range guardCases {
		t.Run(tc.name, func(t *testing.T) {
			users := guardFixtures()
			svc := NewUserService(users, nil)

			_, err := svc.UpdateUserTwoFactor(callerContext("caller", tc.caller),
				connect.NewRequest(&panmailv1.UpdateUserTwoFactorRequest{Id: tc.target}))
			checkGuard(t, tc, err, users.twoFactor)
		})
	}
}

// A tenant administrator could demote or delete a super admin whose home was
// their tenant: the usecase confines the target to the tenant, and the super
// admin is in it.
func TestUpdateUserRoleGuardsSuperAdmins(t *testing.T) {
	for _, tc := range guardCases {
		t.Run(tc.name, func(t *testing.T) {
			users := guardFixtures()
			svc := NewUserService(users, nil)

			_, err := svc.UpdateUserRole(callerContext("caller", tc.caller),
				connect.NewRequest(&panmailv1.UpdateUserRoleRequest{Id: tc.target, Role: panmailv1.UserRole_USER_ROLE_VIEWER}))
			checkGuard(t, tc, err, users.roleChanged)
		})
	}
}

func TestDeleteUserGuardsSuperAdmins(t *testing.T) {
	for _, tc := range guardCases {
		t.Run(tc.name, func(t *testing.T) {
			users := guardFixtures()
			svc := NewUserService(users, nil)

			_, err := svc.DeleteUser(callerContext("caller", tc.caller),
				connect.NewRequest(&panmailv1.DeleteUserRequest{Id: tc.target}))
			checkGuard(t, tc, err, users.deleted)
		})
	}
}
