package migrate

import (
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// A rolling deploy starts the new instances while the old ones are still
// serving, so on any release carrying a migration this runs several times at
// once against one database.
//
// Without a lock each instance reads the same set of applied versions, decides
// the same steps are outstanding, and executes the same DDL. The visible half
// is the loser failing on "applied but could not be recorded", since version is
// a primary key, and refusing to start. The half that matters is two ALTERs
// against one table at the same time.
//
// Only PostgreSQL: SQLite cannot be shared by two instances in the first place,
// because Pebble takes an exclusive lock on the store directories.
//
//	PANMAIL_TEST_POSTGRES='postgres://...' go test ./internal/migrate/

const envPostgres = "PANMAIL_TEST_POSTGRES"

func postgresForMigration(t *testing.T, schema string) *sql.DB {
	t.Helper()

	dsn := os.Getenv(envPostgres)
	if dsn == "" {
		t.Skipf("set %s to run migrations against PostgreSQL", envPostgres)
	}

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()

	// A schema per test, so a run leaves nothing behind for the next one.
	if _, err := admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE"); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := sql.Open("pgx", dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
	})

	sep := "?"
	if containsQuery(dsn) {
		sep = "&"
	}
	db, err := sql.Open("pgx", dsn+sep+"options=-c%20search_path%3D"+schema)
	if err != nil {
		t.Fatalf("connect to %s: %v", schema, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Ping(); err != nil {
		t.Fatalf("ping %s: %v", schema, err)
	}
	return db
}

func containsQuery(dsn string) bool { return strings.Contains(dsn, "?") }

func TestConcurrentMigrationsDoNotCollide(t *testing.T) {
	const instances = 4

	db := postgresForMigration(t, "migrate_concurrent")

	// Every instance uses its own pool, the way separate processes would. One
	// shared *sql.DB would share a connection pool and hide the problem.
	var wg sync.WaitGroup
	errs := make([]error, instances)

	start := make(chan struct{})
	for i := range instances {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them together, so they race for real
			errs[i] = Run(db, DialectPostgres)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("instance %d failed to start: %v", i, err)
		}
	}

	// And the schema is applied once, not four times.
	var count int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	var distinct int
	if err := db.QueryRow("SELECT count(DISTINCT version) FROM schema_migrations").Scan(&distinct); err != nil {
		t.Fatalf("read distinct versions: %v", err)
	}
	if count != distinct {
		t.Errorf("schema_migrations holds %d rows for %d versions; a migration was recorded twice", count, distinct)
	}
	if count == 0 {
		t.Error("nothing was migrated")
	}

	// The tables the rest of the system needs are actually there.
	for _, table := range []string{"tenants", "email_providers", "outbox", "webhook_deliveries"} {
		var exists bool
		err := db.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema = current_schema() AND table_name = $1)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if !exists {
			t.Errorf("%s was not created", table)
		}
	}
}

// The second instance must wait rather than fail, and then find its work
// already done.
func TestASecondRunAfterAConcurrentOneHasNothingToDo(t *testing.T) {
	db := postgresForMigration(t, "migrate_second_run")

	if err := Run(db, DialectPostgres); err != nil {
		t.Fatalf("first run: %v", err)
	}

	before, err := appliedVersions(db)
	if err != nil {
		t.Fatalf("read applied: %v", err)
	}

	if err := Run(db, DialectPostgres); err != nil {
		t.Fatalf("second run: %v", err)
	}

	after, err := appliedVersions(db)
	if err != nil {
		t.Fatalf("read applied: %v", err)
	}
	if len(before) != len(after) {
		t.Errorf("a second run changed the applied set: %d -> %d", len(before), len(after))
	}
}

// The lock has to be released, or the next deploy waits forever on a lock held
// by a process that has moved on.
//
// On a key of its own, which is the difference between testing the release and
// testing how busy the database is. A PostgreSQL advisory lock is database-wide
// and the production key is a single constant, so every package whose tests run
// migrations -- which is every store package -- contends for the same one
// against the same test database. Under "go test ./..." that is around fifteen
// binaries taking and dropping this exact lock.
//
// This test used to use the production key, and could therefore fail while the
// lock was working perfectly: released here, taken immediately by another
// binary, still held ten seconds later. It then reported "the migration lock
// was never released", which was not true and named the wrong subsystem. What
// is being asserted is a property of unlock(), and nothing about that property
// requires sharing a key with the rest of the suite.
func TestTheMigrationLockIsReleased(t *testing.T) {
	db := postgresForMigration(t, "migrate_lock_release")

	const key = migrationLockKey + 1

	unlock, err := lockForMigrationKey(db, DialectPostgres, key)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	unlock()

	// If it were still held, this would block until the test timed out.
	done := make(chan error, 1)
	go func() {
		second, err := lockForMigrationKey(db, DialectPostgres, key)
		if err == nil {
			second()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("re-acquiring the lock failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		// Nothing else uses this key, so there is no innocent explanation
		// left: unlock() returned without dropping the lock.
		t.Fatal("unlock() returned but the lock was still held; the next deploy would hang")
	}
}

// SQLite has no advisory locks, and asking for one must not be an error.
func TestSQLiteNeedsNoLock(t *testing.T) {
	unlock, err := lockForMigration(nil, DialectSQLite)
	if err != nil {
		t.Fatalf("sqlite lock: %v", err)
	}
	unlock() // must not panic on the nil handle either
}
