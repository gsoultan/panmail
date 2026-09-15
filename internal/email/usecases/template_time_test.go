package usecases

import (
	"strings"
	"testing"
	"time"
)

// Go's layout is the reference date itself, which is the one thing about
// time.Format nobody guesses right. Every row here is a layout somebody would
// plausibly type; none of them may render a wrong date.
func TestGoLayoutAcceptsEveryDialect(t *testing.T) {
	// Tuesday, 1 December 2026, 09:30:05 UTC.
	when := time.Date(2026, time.December, 1, 9, 30, 5, 0, time.UTC)

	tests := []struct {
		name   string
		layout string
		want   string
	}{
		{"go reference date", "2006-01-02", "2026-12-01"},
		{"go reference with time", "2006-01-02 15:04:05", "2026-12-01 09:30:05"},
		{"go reference month name", "Jan 2, 2006", "Dec 1, 2026"},
		{"go reference long month", "January 2, 2006", "December 1, 2026"},
		{"go reference weekday", "Mon, 02 Jan 2006", "Tue, 01 Dec 2026"},
		{"go reference time only", "15:04", "09:30"},
		{"go reference kitchen", "3:04PM", "9:30AM"},
		{"go rfc3339 constant", time.RFC3339, "2026-12-01T09:30:05Z"},

		{"example date", "2026-12-01", "2026-12-01"},
		{"example date any year", "1999-05-04", "2026-12-01"},
		{"example datetime", "2026-12-01 09:30:05", "2026-12-01 09:30:05"},
		{"example slashes", "12/01/2026", "12/01/2026"},
		{"example day first", "25/12/2026", "01/12/2026"},
		{"example month name", "December 1, 2026", "December 1, 2026"},
		{"example short month", "Dec 1, 2026", "Dec 1, 2026"},

		{"tokens iso", "YYYY-MM-DD", "2026-12-01"},
		{"tokens day first", "DD/MM/YYYY", "01/12/2026"},
		{"tokens with time", "DD/MM/YYYY HH:mm", "01/12/2026 09:30"},
		{"tokens month name", "MMM DD, YYYY", "Dec 01, 2026"},
		{"tokens long month", "MMMM D, YYYY", "December 1, 2026"},
		{"tokens weekday", "DDD, DD MMM YYYY", "Tue, 01 Dec 2026"},
		{"tokens twelve hour", "hh:mm A", "09:30 AM"},
		{"tokens two digit year", "DD.MM.YY", "01.12.26"},

		{"named date", "date", "2026-12-01"},
		{"named datetime", "datetime", "2026-12-01 09:30:05"},
		{"named time", "time", "09:30:05"},
		{"named iso", "iso", "2026-12-01T09:30:05Z"},
		{"named rfc3339 any case", "RFC3339", "2026-12-01T09:30:05Z"},
		{"named long", "long", "December 1, 2026"},
		{"named short", "short", "Dec 1, 2026"},

		{"empty falls back to iso", "", "2026-12-01T09:30:05Z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := when.Format(goLayout(tc.layout))
			if got != tc.want {
				t.Errorf("Format(%q) = %q, want %q", tc.layout, got, tc.want)
			}
		})
	}
}

// The dialects must not overlap: a layout Go already understands has to keep
// meaning exactly what it meant, or upgrading changes dates in live templates.
func TestGoLayoutLeavesGoLayoutsAlone(t *testing.T) {
	for _, layout := range []string{
		time.RFC3339, time.RFC3339Nano, time.RFC1123, time.RFC1123Z,
		time.ANSIC, time.Kitchen, time.Stamp, "2006-01-02", "15:04:05",
		"Mon Jan _2 15:04:05 MST 2006", "02 Jan 06 15:04 -0700",
	} {
		reference := time.Date(2006, time.January, 2, 15, 4, 5, 0, time.UTC)
		want := reference.Format(layout)
		got := reference.Format(goLayout(layout))
		if got != want {
			t.Errorf("layout %q was rewritten: got %q, want %q", layout, got, want)
		}
	}
}

func TestParseTimestampAcceptsTheShapesCallersSend(t *testing.T) {
	accepted := []string{
		"2026-12-01T09:30:00Z",
		"2026-12-01T09:30:00.123456789Z",
		"2026-12-01T09:30:00+07:00",
		"2026-12-01 09:30:00",
		"2026-12-01 09:30",
		"2026-12-01",
		"2026/12/01",
		"Tue, 01 Dec 2026 09:30:00 UTC",
	}
	for _, in := range accepted {
		if _, ok := parseTimestamp(in); !ok {
			t.Errorf("parseTimestamp(%q) should have accepted it", in)
		}
	}

	rejected := []string{
		"", "hello", "12345", "2026", "order-2026-12-01-final",
		"https://example.test/a/2026-12-01",
		"<p>a long body that merely mentions 2026-12-01 somewhere inside it</p>",
	}
	for _, in := range rejected {
		if _, ok := parseTimestamp(in); ok {
			t.Errorf("parseTimestamp(%q) should have rejected it", in)
		}
	}
}

// A value that is not a date must be refused, not rendered as year 1 or as
// nothing at all. Of everything in an email the date is what a recipient acts
// on, so a wrong one is worse than a send that fails.
func TestFormatDateRefusesWhatIsNotADate(t *testing.T) {
	accepted := []struct {
		name  string
		value any
		want  string
	}{
		{"rfc3339 string", "2026-12-01T09:30:00Z", "2026-12-01"},
		{"date only string", "2026-12-01", "2026-12-01"},
		{"time.Time", time.Date(2026, time.December, 1, 9, 30, 0, 0, time.UTC), "2026-12-01"},
		{"unix seconds", float64(1796117400), "2026-12-01"},
		{"unix milliseconds", float64(1796117400000), "2026-12-01"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatDate(tc.value, "date")
			if err != nil {
				t.Fatalf("formatDate(%v): %v", tc.value, err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	refused := []struct {
		name  string
		value any
	}{
		{"missing", nil},
		{"plain text", "Ada"},
		{"empty string", ""},
		{"a number that is no epoch", float64(42)},
		{"microseconds, which could be read as the wrong unit", float64(1796117400000000)},
		{"a bool", true},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := formatDate(tc.value, "date"); err == nil {
				t.Errorf("formatDate(%v) should have failed, rendered %q", tc.value, got)
			}
		})
	}
}

// A failing template names the field, and the field may hold a whole message
// body. The error goes to a log.
func TestFormatDateErrorDoesNotQuoteAWholeBody(t *testing.T) {
	body := strings.Repeat("x", 5000)
	_, err := formatDate(body, "date")
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > 200 {
		t.Errorf("error is %d characters long: %q", len(err.Error()), err.Error())
	}
}
