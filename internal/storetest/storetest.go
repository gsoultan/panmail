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
// SQLite is the default because it needs nothing installed. It is emphatically
// not a stand-in for PostgreSQL: the two disagree about operators (ILIKE exists
// on one) and about how a timestamp comes back out, and running only on SQLite
// meant the engine actually recommended for production had no coverage at all.
//
// Set PANMAIL_TEST_POSTGRES to a DSN and the same tests run against PostgreSQL:
//
//	PANMAIL_TEST_POSTGRES='postgres://user:pass@127.0.0.1:5432/db?sslmode=disable' go test ./...
//
// On macOS, run the server under Apple's container CLI the way scripts/dev.sh
// does, and **publish it to loopback**:
//
//	container run -d --name pg -e POSTGRES_USER=u -e POSTGRES_PASSWORD=p \
//	  -e POSTGRES_DB=d -p 15432:5432 docker.io/library/postgres:16-alpine
//	container exec pg pg_isready -U u -d d   # the port opens before the db does
//
// Pointing the DSN at the container's own 192.168.64.x address instead looks
// like it should work — nc reaches it — but every test fails with
//
//	dial tcp 192.168.64.x:5432: connect: no route to host
//
// because macOS grants local network access to system tools and withholds it
// from a freshly compiled test binary. It reads as a broken container and is
// not one. Loopback avoids the permission entirely.
//
// Each test gets its own schema rather than its own database — creating a
// database per test is slow enough to discourage running the matrix at all,
// and a schema gives the same isolation.
package storetest

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	migrator "github.com/gsoultan/panmail/internal/migrate"
	"github.com/gsoultan/panmail/pkg/db"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// EnvPostgresDSN switches these tests onto PostgreSQL when set.
const EnvPostgresDSN = "PANMAIL_TEST_POSTGRES"

// Engine names which database the current run is using, for the handful of
// assertions that legitimately differ between them.
func Engine() string {
	if os.Getenv(EnvPostgresDSN) != "" {
		return "postgres"
	}
	return "sqlite"
}

// newPostgresDB gives the test its own schema on a shared server.
func newPostgresDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()

	// Named after the test so a failure leaves something identifiable behind,
	// and suffixed because a test can run more than once in a package run.
	schema := "t" + strings.ToLower(nonAlphanumeric.ReplaceAllString(t.Name(), "_"))
	if len(schema) > 40 {
		schema = schema[:40]
	}
	schema = fmt.Sprintf("%s_%d", schema, time.Now().UnixNano()%1_000_000)

	// One connection each: these run a statement and close.
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	admin.SetMaxOpenConns(1)
	defer admin.Close()

	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		cleanup, err := sql.Open("pgx", dsn)
		if err != nil {
			return
		}
		cleanup.SetMaxOpenConns(1)
		defer cleanup.Close()
		_, _ = cleanup.Exec("DROP SCHEMA " + schema + " CASCADE")
	})

	// search_path goes in the DSN rather than a SET statement: database/sql
	// pools connections, and a SET applies only to whichever one ran it.
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	scoped := dsn + sep + "options=" + url.QueryEscape("-c search_path="+schema)

	sqlDB, err := sql.Open("pgx", scoped)
	if err != nil {
		t.Fatalf("open postgres schema: %v", err)
	}
	// Bounded because a test has no use for an unlimited pool, not because it
	// is known to fix anything.
	//
	// The suite fails intermittently under `go test ./...` against PostgreSQL:
	// an unrelated package times out at around forty seconds and passes in
	// isolation. It happened three times in three different packages, each
	// looking like a bug in whatever had just changed.
	//
	// The obvious theory was connection exhaustion — `go test` runs one binary
	// per package in parallel up to GOMAXPROCS, fifteen here, each opening its
	// own pools against a server allowing 100. **That theory is wrong.**
	// Sampling pg_stat_activity during a full run showed six connections, and
	// the failure reproduced once with these bounds already in place.
	//
	// So this is hygiene, and the cause is still unknown. Do not read the
	// bounds as a fix, and do not assume the flake is gone because a few runs
	// were green — four consecutive runs passed after the failure that
	// disproved the theory.
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(2)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := migrator.Run(sqlDB, migrator.DialectPostgres); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	// The same fixture tenants the SQLite path gets. Missing them here meant
	// every seed that references a tenant hit a foreign key the SQLite schema
	// enforces too — the difference was only that this path never ran.
	seedTenants(t, sqlDB)
	return sqlDB
}

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// NewDB returns a migrated, empty database scoped to the test.
//
// On disk rather than in memory: `:memory:` gives each pooled connection its
// own private database, so a write on one connection is invisible to the next
// read, which surfaces as tests that pass alone and fail in a package run.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()

	if dsn := os.Getenv(EnvPostgresDSN); dsn != "" {
		return newPostgresDB(t, dsn)
	}

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

// ID turns a readable name into a deterministic UUID.
//
// Fixtures want to say "old-failed" and assert on it afterwards. PostgreSQL
// wants a UUID, and rejects anything else outright; SQLite's VARCHAR(36)
// accepts either. That difference is why fixtures full of "k1" and "prov-1"
// passed indefinitely and then failed the moment the same tests were pointed
// at the engine used in production.
//
// Deterministic so a name maps to the same id every time, which is what lets a
// test seed by name and look up by name.
func ID(name string) string {
	return uuid.NewSHA1(idNamespace, []byte(name)).String()
}

var idNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("panmail/storetest"))
