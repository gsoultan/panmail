package postgres

import (
	"context"
	"testing"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/pkg/db"
)

// Membership decides which tenant's data a signed-in user reaches, so what the
// round trip loses is not a cosmetic field: a role that comes back wrong is a
// user acting with authority they were not granted, and a row that does not
// come back at all is a tenant they cannot enter.

func newMembershipRepos(t *testing.T) (repositories.MembershipRepository, repositories.UserRepository, db.Connection) {
	t.Helper()
	conn := storetest.NewConnection(t)
	return NewMembershipStore(conn), NewStore(conn), conn
}

// seedTenant writes the row the membership foreign key points at. PostgreSQL
// enforces it, so a membership test that skipped this would fail on the insert
// rather than on what it meant to assert.
func seedTenant(t *testing.T, conn db.Connection, id, name string) string {
	t.Helper()
	id = storetest.ID(id)
	_, err := conn.GetDB().ExecContext(context.Background(),
		`INSERT INTO tenants (id, name, created_at, updated_at) VALUES ($1, $2, $3, $4)`,
		id, name, fixedTime, fixedTime)
	if err != nil {
		t.Fatalf("seeding tenant %s: %v", name, err)
	}
	return id
}

func seedUser(t *testing.T, users repositories.UserRepository, id, tenantID, email, role string) *entities.User {
	t.Helper()
	u := &entities.User{
		ID:        storetest.ID(id),
		TenantID:  tenantID,
		Email:     email,
		Password:  "hashed",
		Name:      email,
		Role:      role,
		CreatedAt: fixedTime,
		UpdatedAt: fixedTime,
	}
	if err := users.Create(context.Background(), u); err != nil {
		t.Fatalf("seeding user %s: %v", email, err)
	}
	return u
}

func TestAssignAndGetRoundTripsTheRole(t *testing.T) {
	memberships, users, conn := newMembershipRepos(t)
	ctx := context.Background()

	home := seedTenant(t, conn, "t1", "home")
	guest := seedTenant(t, conn, "t2", "guest")
	user := seedUser(t, users, "u1", home, "a@example.com", entities.RoleAdmin)

	err := memberships.Assign(ctx, &entities.UserTenant{
		UserID: user.ID, TenantID: guest, Role: entities.RoleViewer,
		CreatedAt: fixedTime, UpdatedAt: fixedTime,
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}

	got, err := memberships.Get(ctx, user.ID, guest)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("the membership just written was not found")
	}
	if got.Role != entities.RoleViewer {
		t.Errorf("role = %q, want %q — the role held in the guest tenant, not the home role", got.Role, entities.RoleViewer)
	}
}

// A second assignment is how a super admin changes a guest's role. It must
// update rather than fail on the primary key, or the only way to correct a
// role would be to revoke and re-grant.
func TestAssignTwiceUpdatesTheRole(t *testing.T) {
	memberships, users, conn := newMembershipRepos(t)
	ctx := context.Background()

	home := seedTenant(t, conn, "t1", "home")
	guest := seedTenant(t, conn, "t2", "guest")
	user := seedUser(t, users, "u1", home, "a@example.com", entities.RoleAdmin)

	for _, role := range []string{entities.RoleViewer, entities.RoleEditor} {
		err := memberships.Assign(ctx, &entities.UserTenant{
			UserID: user.ID, TenantID: guest, Role: role,
			CreatedAt: fixedTime, UpdatedAt: fixedTime,
		})
		if err != nil {
			t.Fatalf("assign %s: %v", role, err)
		}
	}

	got, err := memberships.Get(ctx, user.ID, guest)
	if err != nil || got == nil {
		t.Fatalf("get: %v (got %v)", err, got)
	}
	if got.Role != entities.RoleEditor {
		t.Errorf("role = %q, want %q — the second assignment should have replaced the first", got.Role, entities.RoleEditor)
	}
}

// "Not a member" is the ordinary answer for every tenant a user has not been
// assigned to. Returning an error instead would make the middleware unable to
// tell a refusal from a database failure.
func TestGetMissingMembershipIsNotAnError(t *testing.T) {
	memberships, users, conn := newMembershipRepos(t)
	ctx := context.Background()

	home := seedTenant(t, conn, "t1", "home")
	other := seedTenant(t, conn, "t2", "other")
	user := seedUser(t, users, "u1", home, "a@example.com", entities.RoleAdmin)

	got, err := memberships.Get(ctx, user.ID, other)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil for a tenant the user was never assigned to", got)
	}
}

func TestListByUserReturnsEveryTenantWithItsName(t *testing.T) {
	memberships, users, conn := newMembershipRepos(t)
	ctx := context.Background()

	home := seedTenant(t, conn, "t1", "alpha")
	guest := seedTenant(t, conn, "t2", "beta")
	user := seedUser(t, users, "u1", home, "a@example.com", entities.RoleAdmin)

	for tenantID, role := range map[string]string{home: entities.RoleAdmin, guest: entities.RoleViewer} {
		if err := memberships.Assign(ctx, &entities.UserTenant{
			UserID: user.ID, TenantID: tenantID, Role: role,
			CreatedAt: fixedTime, UpdatedAt: fixedTime,
		}); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}

	got, err := memberships.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d memberships, want 2", len(got))
	}
	// Ordered by tenant name, so alpha comes first.
	if got[0].TenantName != "alpha" || got[1].TenantName != "beta" {
		t.Errorf("names = %q, %q; want alpha, beta — the join should carry the tenant name",
			got[0].TenantName, got[1].TenantName)
	}
}

// The point of the whole change: a guest appears in the member list of the
// tenant they were lent to, under the role they hold there, without a second
// account.
func TestListUsersByTenantIncludesGuestsUnderTheirLocalRole(t *testing.T) {
	memberships, users, conn := newMembershipRepos(t)
	ctx := context.Background()

	home := seedTenant(t, conn, "t1", "home")
	guest := seedTenant(t, conn, "t2", "guest")

	native := seedUser(t, users, "u1", guest, "native@example.com", entities.RoleAdmin)
	visitor := seedUser(t, users, "u2", home, "visitor@example.com", entities.RoleAdmin)

	for _, m := range []*entities.UserTenant{
		{UserID: native.ID, TenantID: guest, Role: entities.RoleAdmin},
		{UserID: visitor.ID, TenantID: guest, Role: entities.RoleViewer},
	} {
		m.CreatedAt, m.UpdatedAt = fixedTime, fixedTime
		if err := memberships.Assign(ctx, m); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}

	listed, _, err := users.ListByTenantID(ctx, guest, 20, "")
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("got %d users in the guest tenant, want 2 (its native and the assigned visitor)", len(listed))
	}

	byEmail := map[string]*entities.User{}
	for _, u := range listed {
		byEmail[u.Email] = u
	}
	if got := byEmail["visitor@example.com"]; got == nil {
		t.Fatal("the assigned user is missing from the tenant they were assigned to")
	} else if got.Role != entities.RoleViewer {
		t.Errorf("visitor role = %q, want %q — an administrator at home is only a viewer here", got.Role, entities.RoleViewer)
	}
}

// A user is not deleted while rows still point at them, and SQLite does not
// enforce the foreign key unless asked, so the sweep has to be explicit.
func TestRemoveAllForUserClearsEveryMembership(t *testing.T) {
	memberships, users, conn := newMembershipRepos(t)
	ctx := context.Background()

	home := seedTenant(t, conn, "t1", "home")
	guest := seedTenant(t, conn, "t2", "guest")
	user := seedUser(t, users, "u1", home, "a@example.com", entities.RoleAdmin)

	for _, tenantID := range []string{home, guest} {
		if err := memberships.Assign(ctx, &entities.UserTenant{
			UserID: user.ID, TenantID: tenantID, Role: entities.RoleViewer,
			CreatedAt: fixedTime, UpdatedAt: fixedTime,
		}); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}

	if err := memberships.RemoveAllForUser(ctx, user.ID); err != nil {
		t.Fatalf("remove all: %v", err)
	}

	remaining, err := memberships.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("got %d memberships after the sweep, want 0", len(remaining))
	}
}
