package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// A key of its own, for the reason TestTheMigrationLockIsReleased gives: the
// production key is shared with every package's tests.
const transactionTestKey = migrationLockKey + 2

// engines runs a test against SQLite always, and against PostgreSQL when
// PANMAIL_TEST_POSTGRES names one. The behaviour under test differs most on
// PostgreSQL, where an error aborts the surrounding transaction.
func engines(t *testing.T, schema string, test func(t *testing.T, db *sql.DB, dialect string)) {
	t.Run(DialectSQLite, func(t *testing.T) { test(t, newTestDB(t), DialectSQLite) })
	t.Run(DialectPostgres, func(t *testing.T) { test(t, postgresForMigration(t, schema), DialectPostgres) })
}

func tableExists(db *sql.DB, table string) bool {
	_, err := db.Exec("SELECT COUNT(*) FROM " + table)
	return err == nil
}

// A step that failed partway used to leave its earlier statements applied and
// itself unrecorded. The next boot re-ran it against a schema that was neither
// the old one nor the new one, and the gateway, which only logged the failure,
// served that in the meantime.
func TestAFailedStepLeavesNothingBehind(t *testing.T) {
	engines(t, "migrate_failed_step", func(t *testing.T, db *sql.DB, dialect string) {
		ctx := context.Background()
		steps := []migration{
			{version: 1, name: "good", body: "CREATE TABLE tx_good (id TEXT);"},
			{version: 2, name: "half", body: "CREATE TABLE tx_half (id TEXT); THIS IS NOT SQL;"},
		}

		if err := run(ctx, db, dialect, transactionTestKey, steps); err == nil {
			t.Fatal("a step containing a syntax error was reported as applied")
		}

		if !tableExists(db, "tx_good") {
			t.Error("the step before the failure was undone too; only the failing step should be")
		}
		if tableExists(db, "tx_half") {
			t.Error("tx_half exists: the failing step was half applied")
		}
		applied, err := appliedVersions(ctx, db)
		if err != nil {
			t.Fatalf("read applied: %v", err)
		}
		if _, ok := applied[2]; ok {
			t.Error("the failed step was recorded as applied")
		}

		// Fixed and run again, it applies from a clean start.
		steps[1].body = "CREATE TABLE tx_half (id TEXT); CREATE TABLE tx_half_2 (id TEXT);"
		if err := run(ctx, db, dialect, transactionTestKey, steps); err != nil {
			t.Fatalf("the corrected step did not apply: %v", err)
		}
		if !tableExists(db, "tx_half_2") {
			t.Error("the corrected step did not run to the end")
		}
	})
}

// Inside a PostgreSQL transaction an error aborts everything after it, so a
// tolerated "already exists" would take the rest of its step down with it
// unless it is undone on its own.
func TestABenignErrorDoesNotAbortTheRestOfTheStep(t *testing.T) {
	engines(t, "migrate_benign_in_tx", func(t *testing.T, db *sql.DB, dialect string) {
		ctx := context.Background()
		steps := []migration{{
			version: 1,
			name:    "repeat",
			body: `CREATE TABLE tx_dup (id TEXT);
				CREATE TABLE tx_dup (id TEXT);
				CREATE TABLE tx_after (id TEXT);`,
		}}

		if err := run(ctx, db, dialect, transactionTestKey, steps); err != nil {
			t.Fatalf("a benign error failed the step: %v", err)
		}
		if !tableExists(db, "tx_after") {
			t.Error("the statement after the benign error did not run")
		}
	})
}

// database.max_open_conns: 1 is a legitimate setting, and with it the lock
// used to hold the only connection while the first step waited on the pool
// for another: the gateway hung at boot, forever, saying nothing.
func TestMigratingWithASingleConnection(t *testing.T) {
	engines(t, "migrate_single_conn", func(t *testing.T, db *sql.DB, dialect string) {
		db.SetMaxOpenConns(1)

		migrations, err := loadMigrations()
		if err != nil {
			t.Fatalf("load: %v", err)
		}

		done := make(chan error, 1)
		go func() {
			done <- run(context.Background(), db, dialect, transactionTestKey, migrations)
		}()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("migration failed: %v", err)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("migrating on a one-connection pool did not finish: the lock and the steps want a connection each")
		}

		applied, err := appliedVersions(context.Background(), db)
		if err != nil {
			t.Fatalf("read applied: %v", err)
		}
		if len(applied) != len(migrations) {
			t.Errorf("recorded %d steps, want %d", len(applied), len(migrations))
		}
	})
}

// Every step runs in a transaction, and PostgreSQL refuses some statements
// inside one. A file that used them would apply on SQLite in every test and
// fail on the first PostgreSQL deploy.
func TestNoMigrationNeedsToRunOutsideATransaction(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	refused := []string{"CONCURRENTLY", "VACUUM", "CREATE DATABASE", "DROP DATABASE", "ALTER SYSTEM"}
	for _, m := range migrations {
		for _, stmt := range splitStatements(typesFor(DialectPostgres).Replace(m.body)) {
			upper := strings.ToUpper(stmt)
			for _, word := range refused {
				if strings.Contains(upper, word) {
					t.Errorf("%04d_%s uses %s, which PostgreSQL will not run inside the transaction each step is applied in: %s",
						m.version, m.name, word, summarize(stmt))
				}
			}
		}
	}
}
