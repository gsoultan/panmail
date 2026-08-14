package migrate

import "strings"

// Dialect names the supported database engines.
const (
	DialectPostgres = "postgres"
	DialectSQLite   = "sqlite"
	DialectMySQL    = "mysql"
	DialectMariaDB  = "mariadb"
)

// Migration files are written against PostgreSQL types and use these tokens
// where an engine needs something different.
const (
	tokenJSON      = "{{JSON}}"
	tokenUUID      = "{{UUID}}"
	tokenTimestamp = "{{TIMESTAMP}}"

	// tokenPGOnly begins a statement that only PostgreSQL needs. It renders as
	// a comment everywhere else, and splitStatements drops comment lines, so
	// the statement simply does not exist for the other engines.
	//
	// SQLite has no ALTER COLUMN ... TYPE and does not need one: it is
	// dynamically typed, so a column declared TEXT already holds whatever a
	// PostgreSQL column has to be widened to accept.
	tokenPGOnly = "{{PG_ONLY}}"
)

// typesFor returns the column types for an engine.
func typesFor(dialect string) *strings.Replacer {
	switch strings.ToLower(dialect) {
	case DialectSQLite, DialectMySQL, DialectMariaDB:
		return strings.NewReplacer(
			tokenJSON, "TEXT",
			tokenUUID, "VARCHAR(36)",
			tokenTimestamp, "DATETIME",
			tokenPGOnly, "--",
		)
	default:
		return strings.NewReplacer(
			tokenJSON, "JSONB",
			tokenUUID, "UUID",
			tokenTimestamp, "TIMESTAMP WITH TIME ZONE",
			tokenPGOnly, "",
		)
	}
}
