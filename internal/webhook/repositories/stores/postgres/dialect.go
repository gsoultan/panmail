package postgres

import (
	"database/sql"
	"fmt"
	"strings"
)

// isPostgres reports whether this handle talks to PostgreSQL. The claim is
// the one statement that has to differ between the engines: it needs row
// locks on PostgreSQL and does not have them on SQLite, where writers
// serialise anyway. The same check as the outbox store's, for the same reason
// -- database/sql does not expose the driver's identity any other way.
func isPostgres(db *sql.DB) bool {
	if db == nil {
		return false
	}
	return strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "stdlib")
}

// claimQuery picks the claim statement the engine needs.
func claimQuery(db *sql.DB) string {
	if isPostgres(db) {
		return claimDueDeliveriesPgQuery
	}
	return claimDueDeliveriesQuery
}
