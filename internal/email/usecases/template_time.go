package usecases

import (
	"fmt"
	"strings"
	"sync"
	"time"

	// tzdata embeds the zone database so that `.In "Asia/Jakarta"` resolves the
	// same everywhere. The published image installs the system one, but the
	// binary is also run outside it, and a zone that loads in development and
	// fails in production would fail it as a send.
	_ "time/tzdata"
)

// formatDate renders a timestamp from template data.
//
// It backs `{{ .created_at.Format "2026-12-01" }}`, which the renderer rewrites
// into a call to this function. A method call is what an author writes, but a
// function is what can be made to answer well: a field that is absent reaches a
// method as Go's untyped nil, and `{{ .missing.Format "date" }}` then renders
// the literal text "<no value>" into the mail rather than failing. Here the
// absence is an argument, and an argument can be checked.
//
// Timestamps arrive as strings, because template data is a structpb.Struct and
// protobuf's JSON mapping has no date type.
func formatDate(value any, layout string, zone ...string) (string, error) {
	when, err := asTime(value)
	if err != nil {
		return "", err
	}
	if len(zone) > 0 && zone[0] != "" {
		loc, err := loadLocation(zone[0])
		if err != nil {
			return "", err
		}
		when = when.In(loc)
	}
	return when.Format(goLayout(layout)), nil
}

// zonedTime is a timestamp read in another location, and backs
// `{{ .start_at.In "Asia/Jakarta" }}`.
//
// It is a type rather than a formatted string so that `.In` can be chained with
// `.Format`, which is how the two read in Go. A plain time.Time would chain too,
// but its Format is Go's own — it would render the example date "2026-12-01" as
// nonsense, and the two halves of one expression would not agree on what a
// layout is.
type zonedTime struct {
	// Embedded, so String is Go's: `{{ .start_at.In "Asia/Jakarta" }}` with no
	// layout is Go's own spelling, and printing anything other than what Go
	// prints would surprise whoever wrote it.
	time.Time
}

// Format shadows the embedded time.Time's so that a layout means the same
// before and after a zone change.
func (t zonedTime) Format(layout string) string {
	return t.Time.Format(goLayout(layout))
}

// inZone moves a timestamp to another location.
func inZone(value any, zone string) (zonedTime, error) {
	when, err := asTime(value)
	if err != nil {
		return zonedTime{}, err
	}
	loc, err := loadLocation(zone)
	if err != nil {
		return zonedTime{}, err
	}
	return zonedTime{when.In(loc)}, nil
}

// locationCache holds the zones a template asked for. time.LoadLocation reads
// the zone database from disk on every call, and a send renders three times.
var locationCache sync.Map

func loadLocation(name string) (*time.Location, error) {
	if cached, ok := locationCache.Load(name); ok {
		return cached.(*time.Location), nil
	}

	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%s is not a known time zone; use an IANA name such as Asia/Jakarta", quoteForError(name))
	}

	// Only zones that loaded are cached, so the map is bounded by the number of
	// real zone names rather than by whatever a template happens to ask for.
	locationCache.Store(name, loc)
	return loc, nil
}

// asTime reads the shapes a timestamp arrives in, and refuses the rest.
//
// It refuses rather than falling back to a zero time because the fallback would
// put 1 January year 1 in a receipt. Of everything in an email, the date is what
// a recipient acts on — a delivery date, an expiry, an invoice period — so a
// date that is merely wrong is worse than a send that fails and gets fixed.
func asTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case nil:
		return time.Time{}, fmt.Errorf("there is no value here to format as a date")
	case time.Time:
		return v, nil
	case *time.Time:
		if v == nil {
			return time.Time{}, fmt.Errorf("there is no value here to format as a date")
		}
		return *v, nil
	case string:
		parsed, ok := parseTimestamp(v)
		if !ok {
			return time.Time{}, fmt.Errorf("%s is not a date panmail can read; send it as RFC 3339, like 2026-12-01T09:30:00Z", quoteForError(v))
		}
		return parsed, nil
	case float64:
		return fromUnix(v)
	case int64:
		return fromUnix(float64(v))
	case int:
		return fromUnix(float64(v))
	default:
		return time.Time{}, fmt.Errorf("a %T is not a date", value)
	}
}

// Unix epoch bounds, in seconds and in milliseconds.
//
// A bare number does not say which unit it is in, and the two are a factor of a
// thousand apart — read seconds as milliseconds and a 2026 date becomes 1970.
// Rather than guess, each unit is accepted only over the range where it can
// mean a plausible date and nothing else can: seconds covers 2001 to 2286, and
// milliseconds the same window. A number between or outside them is refused.
const (
	unixSecondsMin = 1e9
	unixSecondsMax = 1e10
	unixMillisMin  = 1e12
	unixMillisMax  = 1e13
)

func fromUnix(n float64) (time.Time, error) {
	switch {
	case n >= unixSecondsMin && n < unixSecondsMax:
		return time.Unix(int64(n), 0).UTC(), nil
	case n >= unixMillisMin && n < unixMillisMax:
		return time.UnixMilli(int64(n)).UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("%v is not a Unix timestamp in seconds or milliseconds; send the date as RFC 3339, like 2026-12-01T09:30:00Z", n)
	}
}

// quoteForError keeps a template-data value from filling a log line. The field
// named in a failing template may hold a whole message body.
func quoteForError(v string) string {
	const max = 40
	if len(v) > max {
		return fmt.Sprintf("%q...", v[:max])
	}
	return fmt.Sprintf("%q", v)
}

// inputLayouts are the shapes a timestamp arrives in. RFC3339 covers anything
// that went through protobuf's Timestamp; the spaced forms cover a caller who
// passed a database column straight through, and the date-only form a caller
// who never had a clock reading to send.
var inputLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05 -0700 MST",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02",
	time.RFC1123Z,
	time.RFC1123,
}

// parseTimestamp reports whether a string is a timestamp, and reads it if so.
//
// The length bound is what keeps this off the hot path: template data carries
// message bodies and URLs as well as dates, and no layout above can match a
// string outside this range, so most values are rejected on their length alone
// rather than by twelve failed parses.
func parseTimestamp(s string) (time.Time, bool) {
	if len(s) < 10 || len(s) > 40 {
		return time.Time{}, false
	}
	for _, layout := range inputLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// namedLayouts spare an author from writing any layout at all.
var namedLayouts = map[string]string{
	"date":     "2006-01-02",
	"time":     "15:04:05",
	"datetime": "2006-01-02 15:04:05",
	"iso":      time.RFC3339,
	"iso8601":  time.RFC3339,
	"rfc3339":  time.RFC3339,
	"rfc1123":  time.RFC1123,
	"kitchen":  time.Kitchen,
	"short":    "Jan 2, 2006",
	"long":     "January 2, 2006",
	"human":    "Jan 2, 2006 3:04 PM",
}

// dateTokens translate the YYYY-MM-DD dialect, the one a template author is
// most likely to already know from dayjs or strftime.
//
// Order is significant: the scanner takes the first token that matches at a
// position, so MMMM has to be offered before MMM and MMM before MM, or "MMMM"
// reads as two months.
var dateTokens = []struct{ token, layout string }{
	{"YYYY", "2006"},
	{"YY", "06"},
	{"MMMM", "January"},
	{"MMM", "Jan"},
	{"MM", "01"},
	{"M", "1"},
	{"DDDD", "Monday"},
	{"DDD", "Mon"},
	{"DD", "02"},
	{"D", "2"},
	{"HH", "15"},
	{"hh", "03"},
	{"h", "3"},
	{"mm", "04"},
	{"m", "4"},
	{"ss", "05"},
	{"s", "5"},
	{"SSS", "000"},
	{"ZZZ", "MST"},
	{"ZZ", "-0700"},
	{"A", "PM"},
	{"a", "pm"},
}

// tokenLayout translates "DD/MM/YYYY HH:mm", and reports false for anything
// that is not written in that dialect.
//
// The test is deliberately all-or-nothing: every letter in the layout has to
// belong to a token, so a single unrecognised letter rejects the whole string.
// That is what keeps Go's own layouts out of here — "Mon, 02 Jan 2006" gets as
// far as the M and stops at the o, and "3:04 PM" stops at the P. A rule that
// translated whichever tokens it found would rewrite the "mm" inside a month
// name and corrupt layouts that were already correct.
func tokenLayout(layout string) (string, bool) {
	var out strings.Builder
	sawToken := false

	for i := 0; i < len(layout); {
		c := layout[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			out.WriteByte(c)
			i++
			continue
		}

		matched := false
		for _, tk := range dateTokens {
			if strings.HasPrefix(layout[i:], tk.token) {
				out.WriteString(tk.layout)
				i += len(tk.token)
				matched, sawToken = true, true
				break
			}
		}
		if !matched {
			return "", false
		}
	}

	// A layout with no letters at all — "2026-12-01" — is an example date, not a
	// token layout, and must fall through to be read as one.
	return out.String(), sawToken
}

// exampleLayouts are the shapes an example date may be written in, most
// significant field first so that a slash date reads the way Go's own reference
// layout does: 01 is the month and 02 the day. An author who wants the other
// order has DD/MM/YYYY, which says so.
var exampleLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02",
	"01/02/2006 15:04:05",
	"01/02/2006 15:04",
	"01/02/2006",
	"02/01/2006",
	"02-01-2006",
	"01-02-2006",
	"January 2, 2006",
	"January 2 2006",
	"Jan 2, 2006",
	"Jan 2 2006",
	"2 January 2006",
	"2 Jan 2006",
	"15:04:05",
	"15:04",
	"03:04 PM",
	"3:04 PM",
	time.RFC1123Z,
	time.RFC1123,
	"2006",
}

// goLayout turns whatever an author wrote into a layout time.Format understands.
//
// Go's layout is the reference date itself — "2006-01-02" means year-month-day
// because 2006 is the reference year. Write the same shape with any other date,
// which is the obvious thing to try, and Go does not complain: "2026-12-01"
// parses as day, zero-day, a literal 6, month, day, zero-month, and renders
// "1016-121-12". A silently wrong date in a receipt or an expiry notice is the
// one output worth going out of the way to prevent, so an example date is read
// as an example.
//
// Resolution order is named, then token dialect, then example date, then Go's
// own layout unchanged. The orders cannot disagree: a token layout is never a
// valid date, and a Go layout that is also a valid date — "2006-01-02",
// "Jan 2, 2006", "15:04:05" — parses to the reference date and yields itself.
func goLayout(layout string) string {
	trimmed := strings.TrimSpace(layout)
	if trimmed == "" {
		return time.RFC3339
	}
	if named, ok := namedLayouts[strings.ToLower(trimmed)]; ok {
		return named
	}
	if translated, ok := tokenLayout(layout); ok {
		return translated
	}
	for _, candidate := range exampleLayouts {
		if _, err := time.Parse(candidate, layout); err == nil {
			return candidate
		}
	}
	return layout
}
