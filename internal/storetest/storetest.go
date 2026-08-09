// Package storetest provides a real database for repository tests.
//
// The stores are the least-covered layer and the one where being right depends
// on what the database actually does, so these tests run the real embedded SQL
// against a real engine rather than asserting against a stub.
//
// The schema comes from internal/migrate, not from DDL written inside the test.
// A hand-copied schema drifts from the migrations the moment someone adds a
// column, and a store test passing against a schema production does not have is
// worse than no test at all.
//
// SQLite is used because it needs nothing installed and accepts the $N
// positional parameters the embedded queries are written with. It is not a
// stand-in for PostgreSQL everywhere — see the note on ILIKE in
// .serena/memories/multi-db-claim-is-partial.md — but for the CRUD and
// tenant-scoping behaviour these tests assert, the two agree.
package storetest

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	migrator "github.com/gsoultan/panmail/internal/migrate"
	"github.com/gsoultan/panmail/pkg/db"

	_ "modernc.org/sqlite"
)

// NewDB returns a migrated, empty database scoped to the test.
//
// On disk rather than in memory: `:memory:` gives each pooled connection its
// own private database, so a write on one connection is invisible to the next
// read, which surfaces as tests that pass alone and fail in a package run.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "store_test.db")
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := migrator.Run(sqlDB, migrator.DialectSQLite); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	seedTenants(t, sqlDB)

	return sqlDB
}

// seedTenants inserts the two fixture tenants.
//
// Foreign keys are on, and every tenant-scoped table references tenants(id), so
// without these rows a store test fails on the insert rather than on the
// behaviour it meant to assert. Leaving foreign keys off instead would hide the
// case where a row is written against a tenant that does not exist.
func seedTenants(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	for _, id := range []string{TenantA, TenantB} {
		_, err := sqlDB.Exec(
			`INSERT INTO tenants (id, name, retry_pattern, created_at, updated_at) VALUES ($1, $2, $3, $4, $5)`,
			id, "tenant-"+id[:8], "[]", now, now,
		)
		if err != nil {
			t.Fatalf("seed tenant %s: %v", id, err)
		}
	}
}

// NewConnection wraps NewDB in the db.Connection the stores take.
func NewConnection(t *testing.T) db.Connection {
	t.Helper()
	return db.NewConnection(NewDB(t))
}

// TenantA and TenantB are two tenants for isolation assertions. Every store is
// tenant-scoped, and a query that forgets its tenant filter still passes a
// single-tenant test — so nothing here uses only one.
const (
	TenantA = "11111111-1111-1111-1111-111111111111"
	TenantB = "22222222-2222-2222-2222-222222222222"
)
