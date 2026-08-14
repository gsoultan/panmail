package postgres

import (
	"database/sql"
	"strings"
	"time"
)

// timestampLayouts are the shapes a stored timestamp comes back in.
//
// More than one, because the engines and drivers disagree and one of them is
// Go's own String() form — which carries a zone abbreviation and, worse, the
// monotonic clock reading, as in
//
//	2026-08-09 17:15:18.415063 +0700 WIB m=+89.033435334
//
// That is not a format anything can parse as a time, which is why asking the
// driver to scan MIN(created_at) straight into a time.Time fails. Reading it
// as text and parsing here is what makes the metric work against rows already
// in the table.
var timestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999-07:00",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
}

// parseStoredTime reads a timestamp column that arrived as text.
//
// Returns the zero time when there is nothing to read or nothing that parses.
// A caller treats that as "no age to report" rather than as the epoch, since a
// gauge claiming a fifty-year-old backlog is worse than one saying nothing.
func parseStoredTime(v sql.NullString) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	raw := strings.TrimSpace(v.String)
	if raw == "" {
		return time.Time{}
	}

	// Go's String() appends the monotonic reading after the wall clock; it is
	// meaningless outside the process that wrote it.
	//
	// The sign matters and was missed: a time built by subtracting from now
	// renders as "m=-59.9", not "m=+". Stripping only the positive form left
	// every such value unparseable, so the age gauge reported nothing for
	// exactly the rows an operator would be alerting on.
	if i := strings.Index(raw, " m="); i != -1 {
		raw = raw[:i]
	}

	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}
