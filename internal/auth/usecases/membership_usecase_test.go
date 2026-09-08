package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	tenantentities "github.com/gsoultan/panmail/internal/tenant/entities"
)

// Membership is an authorization decision: what these rules allow is exactly
// the set of tenants a signed-in user can reach. Each test below names a way
// the feature could hand out more than was intended.

type fakeMembershipRepo struct {
	rows map[string]*entities.UserTenant
}

func newFakeMembershipRepo() *fakeMembershipRepo {
	return &fakeMembershipRepo{rows: map[string]*entities.UserTenant{}}
}

func membershipKey(userID, tenantID string) string { return userID + "|" + tenantID }

func (r *fakeMembershipRepo) Assign(ctx context.Context, m *entities.UserTenant) error {
	copied := *m
	r.rows[membershipKey(m.UserID, m.TenantID)] = &copied
	return nil
}

func (r *fakeMembershipRepo) Remove(ctx context.Context, userID, tenantID string) error {
	delete(r.rows, membershipKey(userID, tenantID))
	return nil
}

func (r *fakeMembershipRepo) Get(ctx context.Context, userID, tenantID string) (*entities.UserTenant, error) {
	m, ok := r.rows[membershipKey(userID, tenantID)]
	if !ok {
		return nil, nil
	}
	return m, nil
}

func (r *fakeMembershipRepo) ListByUser(ctx context.Context, userID string) ([]*entities.UserTenant, error) {
	var out []*entities.UserTenant
	for _, m := range r.rows {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeMembershipRepo) UpdateRole(ctx context.Context, userID, tenantID, role string) error {
	if m, ok := r.rows[membershipKey(userID, tenantID)]; ok {
		m.Role = role
	}
	return nil
}

func (r *fakeMembershipRepo) RemoveAllForUser(ctx context.Context, userID string) error {
	for key, m := range r.rows {
		if m.UserID == userID {
			delete(r.rows, key)
		}
	}
	return nil
}

// knownTenantRepo answers for the tenants a test has declared, so that
// "tenant does not exist" is distinguishable from "tenant exists".
type knownTenantRepo struct{ names map[string]string }

func (r *knownTenantRepo) Create(ctx context.Context, t *tenantentities.Tenant) error { return nil }
func (r *knownTenantRepo) GetByID(ctx context.Context, id string) (*tenantentities.Tenant, error) {
	name, ok := r.names[id]
	if !ok {
		return nil, nil
	}
	return &tenantentities.Tenant{ID: id, Name: name}, nil
}
func (r *knownTenantRepo) List(ctx context.Context, pageSize int, pageToken string) ([]*tenantentities.Tenant, string, error) {
	return nil, "", nil
}
func (r *knownTenantRepo) Update(ctx context.Context, t *tenantentities.Tenant) error { return nil }
func (r *knownTenantRepo) Delete(ctx context.Context, id string) error                { return nil }

// recordingUserRepo tracks whether users.role was written, which is the
// difference between changing somebody's authority everywhere and changing it
// in one tenant.
type recordingUserRepo struct {
	*stubUserRepo
	roleWrites map[string]string
}

func newRecordingUserRepo(users ...*entities.User) *recordingUserRepo {
	return &recordingUserRepo{stubUserRepo: newStubUserRepo(users...), roleWrites: map[string]string{}}
}

func (r *recordingUserRepo) UpdateRole(ctx context.Context, id, role string) error {
	r.roleWrites[id] = role
	if u, ok := r.byID[id]; ok {
		u.Role = role
	}
	return nil
}

func newMembershipUsecase(t *testing.T, users *recordingUserRepo, memberships *fakeMembershipRepo, tenants map[string]string) MembershipUsecase {
	t.Helper()
	return NewMembershipUsecase(memberships, users, &knownTenantRepo{names: tenants})
}

func user(id, tenantID, role string) *entities.User {
	return &entities.User{
		ID: id, TenantID: tenantID, Email: id + "@example.com",
		Role: role, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
}

func TestAssignGrantsTheRequestedRoleInTheTargetTenant(t *testing.T) {
	users := newRecordingUserRepo(user("u1", "home", entities.RoleAdmin))
	memberships := newFakeMembershipRepo()
	uc := newMembershipUsecase(t, users, memberships, map[string]string{"home": "Home", "guest": "Guest"})

	got, err := uc.AssignUserToTenant(context.Background(), "u1", "guest", entities.RoleViewer)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got.Role != entities.RoleViewer || got.TenantName != "Guest" {
		t.Errorf("got role %q in %q, want viewer in Guest", got.Role, got.TenantName)
	}
	if got.IsHome {
		t.Error("the guest tenant was reported as the user's home")
	}
	// An assignment elsewhere must not touch the user's authority at home.
	if _, written := users.roleWrites["u1"]; written {
		t.Error("assigning a guest tenant wrote users.role, which is the role held at home")
	}
}

// Super admin reaches procedures that are not scoped to any tenant. Letting it
// be granted as a membership would turn "lend this account to that tenant"
// into a way to mint a global administrator.
func TestAssignRefusesSuperAdminAsAMembership(t *testing.T) {
	users := newRecordingUserRepo(user("u1", "home", entities.RoleViewer))
	memberships := newFakeMembershipRepo()
	uc := newMembershipUsecase(t, users, memberships, map[string]string{"home": "Home", "guest": "Guest"})

	_, err := uc.AssignUserToTenant(context.Background(), "u1", "guest", entities.RoleSuperAdmin)
	if !errors.Is(err, ErrRoleNotAssignable) {
		t.Fatalf("err = %v, want ErrRoleNotAssignable", err)
	}
	if len(memberships.rows) != 0 {
		t.Error("a membership was written despite the role being refused")
	}
}

func TestAssignRefusesAnUnknownTenant(t *testing.T) {
	users := newRecordingUserRepo(user("u1", "home", entities.RoleViewer))
	uc := newMembershipUsecase(t, users, newFakeMembershipRepo(), map[string]string{"home": "Home"})

	_, err := uc.AssignUserToTenant(context.Background(), "u1", "nope", entities.RoleViewer)
	if !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("err = %v, want ErrTenantNotFound", err)
	}
}

// Assigning the home tenant is a role change there, and sign-in reads
// users.role for the home tenant. Writing only the membership row would leave
// the token carrying the old role.
func TestAssignHomeTenantAlsoWritesTheUserRole(t *testing.T) {
	users := newRecordingUserRepo(user("u1", "home", entities.RoleViewer))
	uc := newMembershipUsecase(t, users, newFakeMembershipRepo(), map[string]string{"home": "Home"})

	if _, err := uc.AssignUserToTenant(context.Background(), "u1", "home", entities.RoleEditor); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if users.roleWrites["u1"] != entities.RoleEditor {
		t.Errorf("users.role = %q, want %q — sign-in would otherwise keep issuing the old role",
			users.roleWrites["u1"], entities.RoleEditor)
	}
}

// A super admin's global role outranks any membership. Lowering it as a side
// effect of recording a home membership would be a silent demotion.
func TestAssignHomeTenantLeavesASuperAdminGlobalRoleAlone(t *testing.T) {
	users := newRecordingUserRepo(user("u1", "home", entities.RoleSuperAdmin))
	uc := newMembershipUsecase(t, users, newFakeMembershipRepo(), map[string]string{"home": "Home"})

	if _, err := uc.AssignUserToTenant(context.Background(), "u1", "home", entities.RoleAdmin); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, written := users.roleWrites["u1"]; written {
		t.Error("a super admin was demoted by recording their home membership")
	}
}

// Sign-in lands on the home tenant. Removing that membership would leave an
// account that authenticates into a tenant it is no longer recorded in.
func TestRemoveRefusesTheHomeTenant(t *testing.T) {
	users := newRecordingUserRepo(user("u1", "home", entities.RoleAdmin))
	uc := newMembershipUsecase(t, users, newFakeMembershipRepo(), map[string]string{"home": "Home"})

	err := uc.RemoveUserFromTenant(context.Background(), "u1", "home")
	if !errors.Is(err, ErrHomeTenantRemoval) {
		t.Fatalf("err = %v, want ErrHomeTenantRemoval", err)
	}
}

func TestRemoveRevokesAGuestMembership(t *testing.T) {
	users := newRecordingUserRepo(user("u1", "home", entities.RoleAdmin))
	memberships := newFakeMembershipRepo()
	uc := newMembershipUsecase(t, users, memberships, map[string]string{"home": "Home", "guest": "Guest"})
	ctx := context.Background()

	if _, err := uc.AssignUserToTenant(ctx, "u1", "guest", entities.RoleViewer); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := uc.RemoveUserFromTenant(ctx, "u1", "guest"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got, _ := memberships.Get(ctx, "u1", "guest"); got != nil {
		t.Error("the membership survived its removal")
	}
}

func TestRemoveRefusesATenantTheUserWasNeverIn(t *testing.T) {
	users := newRecordingUserRepo(user("u1", "home", entities.RoleAdmin))
	uc := newMembershipUsecase(t, users, newFakeMembershipRepo(), map[string]string{"home": "Home", "guest": "Guest"})

	err := uc.RemoveUserFromTenant(context.Background(), "u1", "guest")
	if !errors.Is(err, ErrNotAMember) {
		t.Fatalf("err = %v, want ErrNotAMember", err)
	}
}

// RoleIn is what the auth middleware asks on every tenant switch, so this is
// the table that decides who gets in.
func TestRoleIn(t *testing.T) {
	tests := []struct {
		name     string
		user     *entities.User
		assigned map[string]string // tenant -> role
		tenant   string
		want     string
	}{
		{
			name:   "home tenant uses the role sign-in issued",
			user:   user("u1", "home", entities.RoleAdmin),
			tenant: "home",
			want:   entities.RoleAdmin,
		},
		{
			name:     "assigned tenant uses the membership role, not the home role",
			user:     user("u1", "home", entities.RoleAdmin),
			assigned: map[string]string{"guest": entities.RoleViewer},
			tenant:   "guest",
			want:     entities.RoleViewer,
		},
		{
			name:   "an unassigned tenant grants nothing",
			user:   user("u1", "home", entities.RoleAdmin),
			tenant: "stranger",
			want:   "",
		},
		{
			name:   "super admin reaches every tenant without a membership",
			user:   user("u1", "home", entities.RoleSuperAdmin),
			tenant: "stranger",
			want:   entities.RoleSuperAdmin,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			users := newRecordingUserRepo(tc.user)
			memberships := newFakeMembershipRepo()
			tenants := map[string]string{"home": "Home", "guest": "Guest", "stranger": "Stranger"}
			uc := newMembershipUsecase(t, users, memberships, tenants)

			for tenantID, role := range tc.assigned {
				if _, err := uc.AssignUserToTenant(context.Background(), tc.user.ID, tenantID, role); err != nil {
					t.Fatalf("assign: %v", err)
				}
			}

			got, err := uc.RoleIn(context.Background(), tc.user.ID, tc.tenant)
			if err != nil {
				t.Fatalf("RoleIn: %v", err)
			}
			if got != tc.want {
				t.Errorf("RoleIn(%q) = %q, want %q", tc.tenant, got, tc.want)
			}
		})
	}
}

// The home role is users.role, so a listing that echoed the stored membership
// row would disagree with what the user can actually do after a role change.
func TestListUserTenantsReportsTheEffectiveRole(t *testing.T) {
	users := newRecordingUserRepo(user("u1", "home", entities.RoleAdmin))
	memberships := newFakeMembershipRepo()
	uc := newMembershipUsecase(t, users, memberships, map[string]string{"home": "Home", "guest": "Guest"})
	ctx := context.Background()

	// Seed a home membership recording a stale role, then change users.role.
	if err := memberships.Assign(ctx, &entities.UserTenant{UserID: "u1", TenantID: "home", Role: entities.RoleViewer}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := uc.AssignUserToTenant(ctx, "u1", "guest", entities.RoleEditor); err != nil {
		t.Fatalf("assign: %v", err)
	}

	listed, err := uc.ListUserTenants(ctx, "u1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byTenant := map[string]*entities.UserTenant{}
	for _, m := range listed {
		byTenant[m.TenantID] = m
	}
	if got := byTenant["home"]; got == nil || got.Role != entities.RoleAdmin || !got.IsHome {
		t.Errorf("home membership = %+v, want the users.role admin and IsHome true", got)
	}
	if got := byTenant["guest"]; got == nil || got.Role != entities.RoleEditor || got.IsHome {
		t.Errorf("guest membership = %+v, want editor and IsHome false", got)
	}
}
