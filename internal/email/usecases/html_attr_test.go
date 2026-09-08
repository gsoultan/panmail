package usecases

import (
	"fmt"
	"strings"
	"testing"
)

// The cases that made this function necessary, plus the corners that make a
// hand-rolled version of it wrong.
func TestUnescapeHrefValue(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			// The reported bug. "copy" is a legacy named reference and also an
			// ordinary query parameter name.
			name: "copy is a parameter, not a copyright sign",
			raw:  "https://example.com/a?x=1&copy=2",
			want: "https://example.com/a?x=1&copy=2",
		},
		{
			// "reg" for region is the one most likely to be in real campaign links.
			name: "reg is a parameter, not a registered sign",
			raw:  "https://example.com/a?id=1&reg=uk",
			want: "https://example.com/a?id=1&reg=uk",
		},
		{
			name: "other legacy names used as parameters",
			raw:  "https://example.com/a?not=1&para=2&sect=3&times=4&sup=5",
			want: "https://example.com/a?not=1&para=2&sect=3&times=4&sup=5",
		},
		{
			// The case the whole mechanism exists for.
			name: "amp is decoded",
			raw:  "https://example.com/a?x=1&amp;y=2",
			want: "https://example.com/a?x=1&y=2",
		},
		{
			name: "a raw ampersand is left as one",
			raw:  "https://example.com/a?x=1&y=2",
			want: "https://example.com/a?x=1&y=2",
		},
		{
			name: "numeric references are decoded",
			raw:  "https://example.com/a?x=1&#38;y=2",
			want: "https://example.com/a?x=1&y=2",
		},
		{
			// Numeric references are decoded even unterminated; the "=" carve-out
			// applies only to named ones.
			name: "unterminated numeric references are decoded",
			raw:  "https://example.com/a?x=1&#38y=2",
			want: "https://example.com/a?x=1&y=2",
		},
		{
			// Not followed by "=" or an alphanumeric, so it really is the entity.
			name: "a trailing legacy name is decoded",
			raw:  "https://example.com/a?x=1&copy",
			want: "https://example.com/a?x=1©",
		},
		{
			// The match ends at "not", and the next character is alphanumeric, so
			// the semicolon at the end of the run does not make this a reference.
			name: "notit stays literal despite the semicolon",
			raw:  "https://example.com/a?x=1&notit;z",
			want: "https://example.com/a?x=1&notit;z",
		},
		{
			// And this one really is a reference, for the semicolon itself.
			name: "semi is a real reference",
			raw:  "https://example.com/a?x=1&semi;z",
			want: "https://example.com/a?x=1;z",
		},
		{
			name: "no ampersand is returned unchanged",
			raw:  "https://example.com/path/to/thing",
			want: "https://example.com/path/to/thing",
		},
		{
			name: "unknown names are left alone",
			raw:  "https://example.com/a?utm_campaign=1&fbclid=2",
			want: "https://example.com/a?utm_campaign=1&fbclid=2",
		},
		{
			name: "non-ascii survives",
			raw:  "https://example.com/café?x=1&amp;y=2",
			want: "https://example.com/café?x=1&y=2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := unescapeHrefValue(tc.raw); got != tc.want {
				t.Errorf("unescapeHrefValue(%q)\n got: %q\nwant: %q", tc.raw, got, tc.want)
			}
		})
	}
}

// A value carrying the quote that delimits the synthetic tag would be truncated
// by the tokenizer. hrefRegexp cannot capture one, but the function must not
// depend on its only caller to stay safe.
func TestUnescapeHrefValueRefusesToTruncateOnAQuote(t *testing.T) {
	raw := `https://example.com/a?x=1&amp;q="`
	if got := unescapeHrefValue(raw); got != raw {
		t.Errorf("a value containing a quote was rewritten to %q; the URL would be cut short", got)
	}
}

// The corrupting behaviour, stated as a property: no legacy name used as a
// query parameter may be turned into a character.
func TestNoParameterNameBecomesACharacter(t *testing.T) {
	// Every legacy name that is also a plausible parameter, and some that are
	// not, so the list is not tuned to the ones that already pass.
	names := []string{
		"copy", "reg", "not", "para", "sect", "times", "sum", "deg", "sup1",
		"micro", "cent", "pound", "yen", "curren", "shy", "macr", "ordf", "ordm",
		"laquo", "raquo", "amp", "lt", "gt", "quot",
	}
	for _, n := range names {
		raw := "https://example.com/a?" + n + "=1&" + n + "=2"
		got := unescapeHrefValue(raw)
		if got != raw {
			t.Errorf("parameter %q was mangled:\n got: %q\nwant: %q", n, got, raw)
		}
		if strings.Count(got, "=") != strings.Count(raw, "=") {
			t.Errorf("parameter %q lost an %q", n, "=")
		}
	}
}

// The substitution shortcut has to give the tokenizer's answer on every input it
// claims, or it is just a faster version of the bug it replaced.
//
// The corpus is built by combining the pieces that decide the outcome — the
// reference forms, the characters that follow them, and the surrounding URL
// shape — rather than by listing URLs, so it covers combinations nobody thought
// to write down.
func TestFastPathMatchesTheTokenizer(t *testing.T) {
	refs := []string{
		"&amp;", "&amp", "&", "&#38;", "&#38", "&#x26;", "&copy;", "&copy",
		"&reg", "&not", "&notit;", "&semi;", "&sect", "&times", "&lt;", "&gt;",
		"&unknownref;", "&amp;amp;", "&&", "&amp;&amp;",
	}
	follows := []string{"", "=1", "x=1", ";", "=", "&b=2", "/path", "#frag", "%20"}
	shapes := []string{
		"https://example.com/a?p=0%s%s",
		"https://example.com/%s%s",
		"https://example.com/a#%s%s",
		"%s%s",
	}

	checked := 0
	for _, ref := range refs {
		for _, follow := range follows {
			for _, shape := range shapes {
				raw := fmt.Sprintf(shape, ref, follow)
				if strings.Contains(raw, `"`) {
					continue
				}
				checked++

				got := unescapeHrefValue(raw)
				want := unescapeViaTokenizer(raw)
				if strings.IndexByte(raw, '&') < 0 {
					want = raw // the no-ampersand path is a pass-through by design
				}
				if got != want {
					t.Errorf("disagreement on %q\n fast: %q\ntoken: %q\n(onlyAmpReferences=%v)",
						raw, got, want, onlyAmpReferences(raw))
				}
			}
		}
	}
	if checked < 500 {
		t.Fatalf("only %d inputs were compared; the corpus stopped covering anything", checked)
	}
}

// The shortcut must not claim inputs it cannot handle.
func TestOnlyAmpReferences(t *testing.T) {
	yes := []string{
		"https://e.com/a?x=1&amp;y=2", "&amp;", "&amp;&amp;", "&amp;amp;",
		"no ampersand at all", "", "a&amp;b&amp;c",
	}
	no := []string{
		"&copy;", "&#38;", "&amp", "a&b", "&amp;&copy;", "&ampx;", "&am",
	}
	for _, s := range yes {
		if !onlyAmpReferences(s) {
			t.Errorf("onlyAmpReferences(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if onlyAmpReferences(s) {
			t.Errorf("onlyAmpReferences(%q) = true, want false — it would take the shortcut", s)
		}
	}
}
