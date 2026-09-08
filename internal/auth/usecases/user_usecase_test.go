package usecases

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoultan/panmail/internal/auth/entities"
)

// A user id is not a secret: ListUsers hands them out, and since an account
// can now be a member of several tenants the same id is visible from more than
// one console. So every mutation that names a user by id has to be confined to
// the tenant the caller is acting in — otherwise knowing an id is enough to
// change, disable or delete somebody in a tenant you have no standing in.

// deletingUserRepo records deletions so a refusal can be told from a no-op.
type deletingUserRepo struct {
	*recordingUserRepo
	deleted []string
}

func newDeletingUserRepo(users ...*entities.User) *deletingUserRepo {
	return &deletingUserRepo{recordingUserRepo: newRecordingUserRepo(users...)}
}

func (r *deletingUserRepo) Delete(ctx context.Context, id string) error {
	r.deleted = append(r.deleted, id)
	delete(r.byID, id)
	return nil
}

func newUserUsecase(users *deletingUserRepo, memberships *fakeMembershipRepo) UserUsecase {
	return NewUserUsecase(users, memberships)
}

// The gap this closes: before, UpdateUserRole was `UPDATE users SET role`
// keyed on id alone, so an administrator of one tenant could re-role anybody
// in any other.
func TestUpdateUserRoleRefusesAUserFromAnotherTenant(t *testing.T) {
	users := newDeletingUserRepo(user("victim", "tenant-b", entities.RoleViewer))
	uc := newUserUsecase(users, newFakeMembershipRepo())

	err := uc.UpdateUserRole(context.Background(), "tenant-a", "victim", entities.RoleAdmin)
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
	if _, written := users.roleWrites["victim"]; written {
		t.Error("a user in another tenant had their role rewritten")
	}
}

func TestUpdateUserRoleInTheHomeTenantWritesTheUserRole(t *testing.T) {
	users := newDeletingUserRepo(user("u1", "home", entities.RoleViewer))
	memberships := newFakeMembershipRepo()
	if err := memberships.Assign(context.Background(), &entities.UserTenant{UserID: "u1", TenantID: "home", Role: entities.RoleViewer}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	uc := newUserUsecase(users, memberships)

	if err := uc.UpdateUserRole(context.Background(), "home", "u1", entities.RoleEditor); err != nil {
		t.Fatalf("update: %v", err)
	}
	if users.roleWrites["u1"] != entities.RoleEditor {
		t.Errorf("users.role = %q, want editor", users.roleWrites["u1"])
	}
	// The home membership row is kept in step so listings do not disagree with
	// what sign-in will issue.
	if m, _ := memberships.Get(context.Background(), "u1", "home"); m == nil || m.Role != entities.RoleEditor {
		t.Errorf("home membership = %+v, want editor", m)
	}
}

// Re-roling a guest changes what they may do here, and must not follow them
// home.
func TestUpdateUserRoleInAGuestTenantWritesOnlyTheMembership(t *testing.T) {
	users := newDeletingUserRepo(user("u1", "home", entities.RoleAdmin))
	memberships := newFakeMembershipRepo()
	if err := memberships.Assign(context.Background(), &entities.UserTenant{UserID: "u1", TenantID: "guest", Role: entities.RoleViewer}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	uc := newUserUsecase(users, memberships)

	if err := uc.UpdateUserRole(context.Background(), "guest", "u1", entities.RoleEditor); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, written := users.roleWrites["u1"]; written {
		t.Error("re-roling a guest changed their role at home")
	}
	if m, _ := memberships.Get(context.Background(), "u1", "guest"); m == nil || m.Role != entities.RoleEditor {
		t.Errorf("guest membership = %+v, want editor", m)
	}
}

// A super admin's global role overrules any membership, so writing one would
// look like it applied and change nothing.
func TestUpdateUserRoleRefusesToDemoteASuperAdminFromAGuestTenant(t *testing.T) {
	users := newDeletingUserRepo(user("root", "home", entities.RoleSuperAdmin))
	memberships := newFakeMembershipRepo()
	if err := memberships.Assign(context.Background(), &entities.UserTenant{UserID: "root", TenantID: "guest", Role: entities.RoleAdmin}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	uc := newUserUsecase(users, memberships)

	err := uc.UpdateUserRole(context.Background(), "guest", "root", entities.RoleViewer)
	if !errors.Is(err, ErrSuperAdminFromGuestTenant) {
		t.Fatalf("err = %v, want ErrSuperAdminFromGuestTenant", err)
	}
}

func TestDeleteUserRefusesAUserFromAnotherTenant(t *testing.T) {
	users := newDeletingUserRepo(user("victim", "tenant-b", entities.RoleViewer))
	uc := newUserUsecase(users, newFakeMembershipRepo())

	err := uc.DeleteUser(context.Background(), "tenant-a", "victim")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
	if len(users.deleted) != 0 {
		t.Error("a user in another tenant was deleted")
	}
}

// Deleting removes the account itself. An administrator of a tenant somebody
// is only visiting must not be able to do that; revoking the membership is the
// operation that fits.
func TestDeleteUserRefusesAGuestAndPointsAtRemoval(t *testing.T) {
	users := newDeletingUserRepo(user("u1", "home", entities.RoleViewer))
	memberships := newFakeMembershipRepo()
	if err := memberships.Assign(context.Background(), &entities.UserTenant{UserID: "u1", TenantID: "guest", Role: entities.RoleViewer}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	uc := newUserUsecase(users, memberships)

	err := uc.DeleteUser(context.Background(), "guest", "u1")
	if !errors.Is(err, ErrHomeTenantOnlyDeletion) {
		t.Fatalf("err = %v, want ErrHomeTenantOnlyDeletion", err)
	}
	if len(users.deleted) != 0 {
		t.Error("a visiting user's account was deleted by the tenant they were lent to")
	}
}

// The row in users is what the membership foreign key points at, and SQLite
// only cascades when the connection asks it to.
func TestDeleteUserInTheHomeTenantClearsEveryMembership(t *testing.T) {
	users := newDeletingUserRepo(user("u1", "home", entities.RoleViewer))
	memberships := newFakeMembershipRepo()
	ctx := context.Background()
	for _, tenantID := range []string{"home", "guest"} {
		if err := memberships.Assign(ctx, &entities.UserTenant{UserID: "u1", TenantID: tenantID, Role: entities.RoleViewer}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	uc := newUserUsecase(users, memberships)

	if err := uc.DeleteUser(ctx, "home", "u1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(users.deleted) != 1 {
		t.Fatalf("deleted %v, want exactly the one user", users.deleted)
	}
	if remaining, _ := memberships.ListByUser(ctx, "u1"); len(remaining) != 0 {
		t.Errorf("%d memberships outlived the account they belong to", len(remaining))
	}
}

func TestUpdateUserTwoFactorRefusesAUserFromAnotherTenant(t *testing.T) {
	users := newDeletingUserRepo(user("victim", "tenant-b", entities.RoleViewer))
	uc := newUserUsecase(users, newFakeMembershipRepo())

	err := uc.UpdateUserTwoFactor(context.Background(), "tenant-a", "victim", false)
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound — disabling somebody else's second factor is a takeover step", err)
	}
}

// Without a home membership row a freshly created user is missing from their
// own tenant's member list, because that list is now a membership query.
func TestCreateUserRecordsTheHomeMembership(t *testing.T) {
	users := newDeletingUserRepo()
	memberships := newFakeMembershipRepo()
	uc := newUserUsecase(users, memberships)

	created, err := uc.CreateUser(context.Background(), "home", "new@example.com", "correct horse battery", "New", entities.RoleEditor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	m, _ := memberships.Get(context.Background(), created.ID, "home")
	if m == nil {
		t.Fatal("no home membership was recorded for the new user")
	}
	if m.Role != entities.RoleEditor {
		t.Errorf("membership role = %q, want editor", m.Role)
	}
}

// Super admin is not a tenant role, so the row records the strongest role that
// can be held locally rather than a value the membership table refuses.
func TestCreateSuperAdminRecordsAnAdministratorMembership(t *testing.T) {
	users := newDeletingUserRepo()
	memberships := newFakeMembershipRepo()
	uc := newUserUsecase(users, memberships)

	created, err := uc.CreateUser(context.Background(), "home", "root@example.com", "correct horse battery", "Root", entities.RoleSuperAdmin)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	m, _ := memberships.Get(context.Background(), created.ID, "home")
	if m == nil || m.Role != entities.RoleAdmin {
		t.Errorf("membership = %+v, want an administrator row", m)
	}
}

// GetInTenant is what every scoped mutation calls first, so the role it
// reports has to be the one that applies where the caller is acting.
func TestGetInTenantReportsTheLocalRole(t *testing.T) {
	users := newDeletingUserRepo(user("u1", "home", entities.RoleAdmin))
	memberships := newFakeMembershipRepo()
	if err := memberships.Assign(context.Background(), &entities.UserTenant{UserID: "u1", TenantID: "guest", Role: entities.RoleViewer}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	uc := newUserUsecase(users, memberships)

	got, err := uc.GetInTenant(context.Background(), "guest", "u1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Role != entities.RoleViewer {
		t.Errorf("role = %q, want viewer — the role held in the tenant being asked about", got.Role)
	}
}
