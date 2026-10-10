package postgres

import (
	"context"
	"testing"

	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/internal/tenant/repositories"
)

// CountDependents is what lets DeleteTenant refuse with an explanation instead
// of failing on a foreign key. It has to count exactly the rows that would
// block the delete, in the tenant asked about and no other.
func TestCountDependentsCountsWhatWouldBlockTheDelete(t *testing.T) {
	conn := storetest.NewConnection(t)
	repo := NewStore(conn)
	sqlDB := conn.GetDB()
	ctx := context.Background()

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := sqlDB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	const insertUser = `INSERT INTO users (id, tenant_id, email, password, name, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'x', 'n', 'USER_ROLE_ADMIN', $4, $4)`
	const insertMembership = `INSERT INTO user_tenants (user_id, tenant_id, role, created_at, updated_at)
		VALUES ($1, $2, 'USER_ROLE_VIEWER', $3, $3)`

	homeA, homeB := storetest.ID("home-a"), storetest.ID("home-b")
	exec(insertUser, homeA, storetest.TenantA, "a@example.com", fixedTime)
	exec(insertUser, homeB, storetest.TenantB, "b@example.com", fixedTime)
	// Home memberships are part of the user, not something extra to remove.
	exec(insertMembership, homeA, storetest.TenantA, fixedTime)
	exec(insertMembership, homeB, storetest.TenantB, fixedTime)
	// B's user is lent to A: a guest member of A.
	exec(insertMembership, homeB, storetest.TenantA, fixedTime)
	exec(`INSERT INTO api_keys (id, tenant_id, name, key_hash, prefix, created_at, updated_at)
		VALUES ($1, $2, 'k', 'h', 'pm_', $3, $3)`, storetest.ID("key-a"), storetest.TenantA, fixedTime)

	got, err := repo.(repositories.DependentCounter).CountDependents(ctx, storetest.TenantA)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	want := repositories.Dependents{Users: 1, GuestMembers: 1, ApiKeys: 1}
	if got != want {
		t.Errorf("CountDependents(A) = %+v, want %+v", got, want)
	}

	// The refusal is only as good as the count: when it says empty, the
	// delete has to go through.
	empty := tenant(storetest.ID("empty"), "Empty", nil)
	if err := repo.Create(ctx, empty); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err = repo.(repositories.DependentCounter).CountDependents(ctx, empty.ID)
	if err != nil {
		t.Fatalf("count empty: %v", err)
	}
	if got != (repositories.Dependents{}) {
		t.Errorf("CountDependents(empty) = %+v, want zero", got)
	}
	if err := repo.Delete(ctx, empty.ID); err != nil {
		t.Errorf("deleting a tenant the count called empty: %v", err)
	}

	// And when it says not empty, the delete really would fail — which is
	// the bug this count exists to explain.
	if err := repo.Delete(ctx, storetest.TenantA); err == nil {
		t.Error("deleting a tenant with users succeeded; the foreign keys are not enforced here")
	}
}
