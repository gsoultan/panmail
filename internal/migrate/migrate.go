// Package migrate applies the database schema in ordered, recorded steps.
//
// The previous scheme re-ran every CREATE and ALTER on each boot and discarded
// the results with `_, _ = db.Exec(q)`. That made a genuinely failed migration
// indistinguishable from "this column already exists", so schema drift was
// invisible until a query failed in production.
package migrate

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed sql/*.sql
var migrationFS embed.FS

// migration is one ordered step.
type migration struct {
	version int
	name    string
	body    string
}

// Run brings the database up to date, applying only the steps it has not seen.
func Run(db *sql.DB, dialect string) error {
	if db == nil {
		return errors.New("database not connected")
	}

	// One instance migrates; the rest wait and then find nothing to do.
	//
	// A rolling deploy starts the new instances while the old ones are still
	// serving, so on any release carrying a migration this runs several times
	// at once. Without a lock each one reads the same set of applied versions,
	// concludes the same steps are outstanding, and executes the same DDL
	// against the same tables. The visible half of that is the loser failing on
	// "applied but could not be recorded" — version is a primary key — and
	// refusing to start. The half that matters is two ALTERs on one table at
	// the same time.
	unlock, err := lockForMigration(db, dialect)
	if err != nil {
		return err
	}
	defer unlock()

	if err := ensureVersionTable(db, dialect); err != nil {
		return fmt.Errorf("failed to prepare the migration table: %w", err)
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return fmt.Errorf("failed to read applied migrations: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	replacer := typesFor(dialect)
	for _, m := range migrations {
		if _, done := applied[m.version]; done {
			continue
		}

		slog.Info("applying migration", "version", m.version, "name", m.name)
		if err := apply(db, m, replacer); err != nil {
			return fmt.Errorf("migration %04d_%s failed: %w", m.version, m.name, err)
		}
		if err := recordVersion(db, m.version); err != nil {
			return fmt.Errorf("migration %04d_%s applied but could not be recorded: %w", m.version, m.name, err)
		}
	}

	return nil
}

func apply(db *sql.DB, m migration, replacer *strings.Replacer) error {
	for _, stmt := range splitStatements(replacer.Replace(m.body)) {
		if _, err := db.Exec(stmt); err != nil {
			if isBenign(err) {
				slog.Debug("migration statement already satisfied", "version", m.version, "error", err)
				continue
			}
			return fmt.Errorf("%w (statement: %s)", err, summarize(stmt))
		}
	}
	return nil
}

// isBenign reports whether an error means the change is already in place.
//
// "ADD COLUMN IF NOT EXISTS" is not portable — PostgreSQL has it, SQLite does
// not — so an additive column step has to tolerate the column already
// existing. Nothing else is tolerated.
func isBenign(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, benign := range []string{
		"duplicate column name", // SQLite
		"already exists",        // PostgreSQL, and index creation generally
		"duplicate column",      // MySQL / MariaDB
	} {
		if strings.Contains(msg, benign) {
			return true
		}
	}
	return false
}

func ensureVersionTable(db *sql.DB, dialect string) error {
	timestamp := "TIMESTAMP WITH TIME ZONE"
	if strings.ToLower(dialect) != DialectPostgres {
		timestamp = "DATETIME"
	}

	_, err := db.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at %s NOT NULL
		)`, timestamp))
	return err
}

func appliedVersions(db *sql.DB) (map[int]struct{}, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]struct{})
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = struct{}{}
	}
	return applied, rows.Err()
}

func recordVersion(db *sql.DB, version int) error {
	_, err := db.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)", version, time.Now())
	return err
}

// migrationLockKey identifies this lock among any other advisory locks in the
// database. Arbitrary but fixed: every instance has to name the same number or
// they do not exclude each other.
const migrationLockKey = 0x70616e6d // "panm"

// lockForMigration blocks until this process is the one allowed to migrate.
//
// PostgreSQL advisory locks are held by a session, so this takes a dedicated
// connection out of the pool and keeps it for the duration. Using db.Exec would
// acquire the lock on whichever connection the pool handed over and release it
// on whichever one it handed over next — that is, not at all reliably.
//
// The wait is deliberately unbounded. A migration that is genuinely running can
// take as long as it takes, and an instance that gave up waiting would carry on
// and start serving against a schema halfway through changing, which is the
// thing this exists to prevent.
func lockForMigration(db *sql.DB, dialect string) (func(), error) {
	// SQLite has no advisory locks and does not need them: it is a single file
	// with one writer, and two instances cannot share one anyway — Pebble takes
	// an exclusive lock on the store directories long before this matters.
	if strings.ToLower(dialect) != DialectPostgres {
		return func() {}, nil
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to take a connection for the migration lock: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to acquire the migration lock: %w", err)
	}

	return func() {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
			// Not fatal, and not silent: closing the connection ends the
			// session and drops the lock regardless, so the next instance is
			// not stuck — but an operator should know it happened.
			slog.Warn("failed to release the migration lock; closing the connection will drop it", "error", err)
		}
		_ = conn.Close()
	}, nil
}

// loadMigrations reads the embedded files, ordered by their numeric prefix.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations: %w", err)
	}

	var migrations []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		version, name, err := parseName(entry.Name())
		if err != nil {
			return nil, err
		}

		body, err := migrationFS.ReadFile(path.Join("sql", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, migration{version: version, name: name, body: string(body)})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	for i := 1; i < len(migrations); i++ {
		if migrations[i].version == migrations[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", migrations[i].version)
		}
	}

	return migrations, nil
}

// parseName splits "0002_api_key_scopes.sql" into its version and name.
func parseName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")

	prefix, name, found := strings.Cut(base, "_")
	if !found {
		return 0, "", fmt.Errorf("migration %q must be named <version>_<name>.sql", filename)
	}

	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, "", fmt.Errorf("migration %q has a non-numeric version prefix", filename)
	}

	return version, name, nil
}

// splitStatements breaks a file into individual statements.
//
// Statements are separated by semicolons and comment lines are dropped. No
// statement in this package's files contains a semicolon inside a literal; if
// one ever needs to, this splitter must be replaced rather than worked around.
func splitStatements(body string) []string {
	var stripped strings.Builder
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteString("\n")
	}

	var statements []string
	for stmt := range strings.SplitSeq(stripped.String(), ";") {
		if trimmed := strings.TrimSpace(stmt); trimmed != "" {
			statements = append(statements, trimmed)
		}
	}
	return statements
}

// summarize shortens a statement for an error message.
func summarize(stmt string) string {
	flat := strings.Join(strings.Fields(stmt), " ")
	const limit = 120
	if len(flat) > limit {
		return flat[:limit] + "…"
	}
	return flat
}
