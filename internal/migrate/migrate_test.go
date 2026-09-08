package migrate

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migrate_test.db"))
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func columns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()

	rows, err := db.Query("SELECT name FROM pragma_table_info($1)", table)
	if err != nil {
		t.Fatalf("failed to inspect %s: %v", table, err)
	}
	defer rows.Close()

	found := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("failed to scan column: %v", err)
		}
		found[name] = true
	}
	return found
}

func TestRunCreatesTheFullSchema(t *testing.T) {
	db := newTestDB(t)

	if err := Run(db, DialectSQLite); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	tables := []string{
		"tenants", "users", "email_providers", "api_keys",
		"templates", "suppressions", "webhooks", "outbox", "schema_migrations",
		// The SMTP submission listener is stored rather than passed as a flag,
		// so an administrator can open the door from the dashboard. It is its
		// own table and not more columns on system_settings because it holds a
		// TLS private key, and system_settings is served to every authenticated
		// caller.
		"smtp_submission",
	}
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			if len(columns(t, db, table)) == 0 {
				t.Errorf("table %s was not created", table)
			}
		})
	}
}

// The columns added after versioning was introduced must actually arrive —
// this is what the old error-discarding ALTER block could not guarantee.
func TestRunAppliesLaterMigrations(t *testing.T) {
	db := newTestDB(t)

	if err := Run(db, DialectSQLite); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	tests := []struct {
		table  string
		column string
	}{
		{"api_keys", "scopes"},
		{"email_providers", "webhook_secret"},
		{"outbox", "claim_token"},
		{"outbox", "claimed_until"},
	}

	for _, tc := range tests {
		t.Run(tc.table+"."+tc.column, func(t *testing.T) {
			if !columns(t, db, tc.table)[tc.column] {
				t.Errorf("%s.%s is missing", tc.table, tc.column)
			}
		})
	}
}

func TestRunIsIdempotent(t *testing.T) {
	db := newTestDB(t)

	for i := range 3 {
		if err := Run(db, DialectSQLite); err != nil {
			t.Fatalf("run %d failed: %v", i+1, err)
		}
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("failed to count migrations: %v", err)
	}

	expected, err := loadMigrations()
	if err != nil {
		t.Fatalf("failed to load migrations: %v", err)
	}
	if count != len(expected) {
		t.Errorf("recorded %d migrations after three runs; want %d", count, len(expected))
	}
}

// An installation created by the previous scheme already has every column but
// no schema_migrations table. Migrating it must succeed and leave the data
// alone rather than failing on duplicate columns.
func TestRunUpgradesAPreVersioningDatabase(t *testing.T) {
	db := newTestDB(t)

	// Stand in for the old boot path: schema present, nothing recorded.
	if err := Run(db, DialectSQLite); err != nil {
		t.Fatalf("initial migration failed: %v", err)
	}
	if _, err := db.Exec("INSERT INTO tenants (id, name, created_at, updated_at) VALUES ('t1', 'Existing', '2026-01-01', '2026-01-01')"); err != nil {
		t.Fatalf("failed to seed: %v", err)
	}
	if _, err := db.Exec("DROP TABLE schema_migrations"); err != nil {
		t.Fatalf("failed to drop version table: %v", err)
	}

	if err := Run(db, DialectSQLite); err != nil {
		t.Fatalf("re-migration of an existing database failed: %v", err)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM tenants WHERE id = 't1'").Scan(&name); err != nil {
		t.Fatalf("existing data was lost: %v", err)
	}
	if name != "Existing" {
		t.Errorf("expected the existing row to survive, got %q", name)
	}
}

// A real failure must surface. The old code could not tell one from a
// duplicate column, which is the whole reason for this package.
func TestRealFailuresAreReported(t *testing.T) {
	db := newTestDB(t)

	err := apply(db, migration{
		version: 999,
		name:    "broken",
		body:    "CREATE TABLE valid_one (id TEXT); THIS IS NOT SQL;",
	}, typesFor(DialectSQLite))

	if err == nil {
		t.Fatal("expected a syntax error to be reported")
	}
	if !strings.Contains(err.Error(), "THIS IS NOT SQL") {
		t.Errorf("expected the failing statement in the error, got: %v", err)
	}
}

func TestBenignErrorsAreTolerated(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"sqlite duplicate column", errors.New("duplicate column name: scopes"), true},
		{"postgres duplicate column", errors.New(`column "scopes" of relation "api_keys" already exists`), true},
		{"mysql duplicate column", errors.New("Duplicate column name 'scopes'"), true},
		{"syntax error", errors.New(`near "THIS": syntax error`), false},
		{"missing table", errors.New("no such table: api_keys"), false},
		{"permission denied", errors.New("permission denied for table api_keys"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBenign(tc.err); got != tc.want {
				t.Errorf("isBenign(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestMigrationsAreOrderedAndUnique(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("failed to load migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations were embedded")
	}

	for i := 1; i < len(migrations); i++ {
		if migrations[i].version <= migrations[i-1].version {
			t.Errorf("migration %d is not after %d", migrations[i].version, migrations[i-1].version)
		}
	}
}

func TestSplitStatementsDropsComments(t *testing.T) {
	got := splitStatements(`
-- a leading comment
CREATE TABLE a (id TEXT);
-- another comment
ALTER TABLE a ADD COLUMN b TEXT;
`)

	if len(got) != 2 {
		t.Fatalf("expected 2 statements, got %d: %#v", len(got), got)
	}
	for _, stmt := range got {
		if strings.Contains(stmt, "--") {
			t.Errorf("comment leaked into statement: %q", stmt)
		}
	}
}

func TestDialectTypes(t *testing.T) {
	tests := []struct {
		dialect string
		want    string
	}{
		{DialectPostgres, "JSONB"},
		{DialectSQLite, "TEXT"},
		{DialectMySQL, "TEXT"},
	}

	for _, tc := range tests {
		t.Run(tc.dialect, func(t *testing.T) {
			got := typesFor(tc.dialect).Replace("col " + tokenJSON)
			if !strings.Contains(got, tc.want) {
				t.Errorf("typesFor(%s) produced %q; want it to contain %q", tc.dialect, got, tc.want)
			}
			if strings.Contains(got, "{{") {
				t.Errorf("token left unreplaced: %q", got)
			}
		})
	}
}
