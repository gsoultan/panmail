package postgres

import (
	"database/sql"
	"fmt"
	"strings"
)

// isPostgres reports whether this handle talks to PostgreSQL.
//
// The stores are written against one SQL dialect and run on two engines, which
// is fine until a statement means something different on each. Claiming is
// that case: it needs row locks on PostgreSQL and does not have them on SQLite,
// where writers serialise anyway.
//
// Determined from the driver rather than by asking the database, so it costs
// nothing and cannot fail at an awkward moment. It is a type name rather than
// something more principled because database/sql does not expose the driver's
// identity any other way.
func isPostgres(db *sql.DB) bool {
	if db == nil {
		return false
	}
	return strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "stdlib")
}

// claimQuery picks the claim statement the engine needs.
func claimQuery(db *sql.DB) string {
	if isPostgres(db) {
		return claimPendingOutboxPgQuery
	}
	return claimPendingOutboxQuery
}
