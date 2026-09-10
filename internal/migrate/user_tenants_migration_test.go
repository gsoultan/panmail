package migrate

import (
	"database/sql"
	"testing"
)

// 0017 introduces membership. Its backfill is the part that cannot be retried
// by hand: an upgrade that creates the table but leaves it empty gives every
// existing user a tenant they can sign in to and no recorded membership of,
// so their own tenant's member list comes back empty.

// rewind removes a migration's recorded version so Run applies it again,
// standing in for an installation that has not seen it yet.
func rewind(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", version); err != nil {
		t.Fatalf("rewinding to before migration %d: %v", version, err)
	}
}

func TestUserTenantsBackfillGivesExistingUsersTheirHomeMembership(t *testing.T) {
	db := newTestDB(t)
	if err := Run(db, DialectSQLite); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// A user as they existed before membership: a row in users, nothing else.
	if _, err := db.Exec(`INSERT INTO tenants (id, name, created_at, updated_at)
		VALUES ('t1', 'Existing', '2026-01-01', '2026-01-01')`); err != nil {
		t.Fatalf("seeding tenant: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, tenant_id, email, password, name, role, created_at, updated_at)
		VALUES ('u1', 't1', 'a@example.com', 'hash', 'A', 'USER_ROLE_EDITOR', '2026-01-01', '2026-01-01')`); err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	if _, err := db.Exec("DELETE FROM user_tenants"); err != nil {
		t.Fatalf("clearing memberships: %v", err)
	}

	rewind(t, db, 17)
	if err := Run(db, DialectSQLite); err != nil {
		t.Fatalf("re-applying the membership migration: %v", err)
	}

	var role string
	err := db.QueryRow("SELECT role FROM user_tenants WHERE user_id = 'u1' AND tenant_id = 't1'").Scan(&role)
	if err != nil {
		t.Fatalf("the existing user has no membership of their own tenant: %v", err)
	}
	// The role carries across, or every upgraded administrator would find
	// themselves demoted to the column default.
	if role != "USER_ROLE_EDITOR" {
		t.Errorf("backfilled role = %q, want USER_ROLE_EDITOR", role)
	}
}

// Re-running the backfill against rows it already created must not fail on the
// primary key: a database restored from a partially-migrated backup, or one
// whose schema_migrations was lost, re-applies every step.
func TestUserTenantsBackfillIsRepeatable(t *testing.T) {
	db := newTestDB(t)
	if err := Run(db, DialectSQLite); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tenants (id, name, created_at, updated_at)
		VALUES ('t1', 'Existing', '2026-01-01', '2026-01-01')`); err != nil {
		t.Fatalf("seeding tenant: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, tenant_id, email, password, name, role, created_at, updated_at)
		VALUES ('u1', 't1', 'a@example.com', 'hash', 'A', 'USER_ROLE_ADMIN', '2026-01-01', '2026-01-01')`); err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	for range 2 {
		rewind(t, db, 17)
		if err := Run(db, DialectSQLite); err != nil {
			t.Fatalf("re-applying the membership migration: %v", err)
		}
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM user_tenants WHERE user_id = 'u1'").Scan(&count); err != nil {
		t.Fatalf("counting memberships: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d membership rows after repeated backfills, want 1", count)
	}
}
