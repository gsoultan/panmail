package postgres

import (
	"database/sql"
	"strings"
	"time"
)

// Timestamps come back as text, and in more than one shape: the engines and
// drivers disagree, and one of the shapes is Go's own String() form, which
// carries a zone abbreviation and the monotonic clock reading. Scanning that
// straight into a time.Time fails.
var timestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999-07:00",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
}

// parseStoredTime returns the zero time when there is nothing to read or
// nothing that parses, which a caller reads as "no age to report" — a gauge
// claiming a fifty-year-old backlog is worse than one saying nothing.
func parseStoredTime(v sql.NullString) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	raw := strings.TrimSpace(v.String)
	if raw == "" {
		return time.Time{}
	}
	// The sign matters: a time built by subtracting from now renders as
	// "m=-59.9", not "m=+".
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
