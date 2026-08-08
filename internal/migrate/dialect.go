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
)

// typesFor returns the column types for an engine.
func typesFor(dialect string) *strings.Replacer {
	switch strings.ToLower(dialect) {
	case DialectSQLite, DialectMySQL, DialectMariaDB:
		return strings.NewReplacer(
			tokenJSON, "TEXT",
			tokenUUID, "VARCHAR(36)",
			tokenTimestamp, "DATETIME",
		)
	default:
		return strings.NewReplacer(
			tokenJSON, "JSONB",
			tokenUUID, "UUID",
			tokenTimestamp, "TIMESTAMP WITH TIME ZONE",
		)
	}
}
