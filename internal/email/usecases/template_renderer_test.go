package usecases

import (
	"strings"
	"testing"
)

// Handlebars escapes `{{x}}` unconditionally and raymond succeeds for almost
// every template, so the isHTML flag — which used to be consulted only in the
// Go-template fallback — was in practice never reached. Every plain-text body
// and every subject line came out HTML-escaped.
func TestPlainTextIsNotHTMLEscaped(t *testing.T) {
	r := NewTemplateRenderer()

	tests := []struct {
		name string
		tpl  string
		data map[string]any
		want string
	}{
		{
			name: "ampersand in a company name",
			tpl:  "{{company}} Summer Sale",
			data: map[string]any{"company": "Tom & Jerry"},
			want: "Tom & Jerry Summer Sale",
		},
		{
			name: "angle brackets and quotes",
			tpl:  "{{note}}",
			data: map[string]any{"note": `5 < 10 > 2 "quoted" 'single'`},
			want: `5 < 10 > 2 "quoted" 'single'`,
		},
		{
			name: "nested value",
			tpl:  "{{order.seller}}",
			data: map[string]any{"order": map[string]any{"seller": "Ben & Co"}},
			want: "Ben & Co",
		},
		{
			name: "value inside a loop",
			tpl:  "{{#each items}}{{this}};{{/each}}",
			data: map[string]any{"items": []any{"A & B", "C & D"}},
			want: "A & B;C & D;",
		},
		{
			name: "numbers and booleans are untouched",
			tpl:  "{{count}}/{{active}}",
			data: map[string]any{"count": 3.0, "active": true},
			want: "3/true",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Render(tc.tpl, tc.data, false)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got != tc.want {
				t.Errorf("plain text was escaped\n got: %q\nwant: %q", got, tc.want)
			}
			if strings.Contains(got, "&amp;") || strings.Contains(got, "&lt;") || strings.Contains(got, "&#34;") {
				t.Errorf("output still contains HTML entities: %q", got)
			}
		})
	}
}

// The subject is rendered as plain text, and no mail client decodes entities in
// one, so this is the most visible form of the bug.
func TestSubjectKeepsItsAmpersand(t *testing.T) {
	r := NewTemplateRenderer()
	got, err := r.Render("Your {{brand}} receipt", map[string]any{"brand": "Ben & Jerry's"}, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "Your Ben & Jerry's receipt" {
		t.Errorf("got %q", got)
	}
}

// The fix must not weaken HTML rendering: a value interpolated into an HTML
// body still has to be escaped, or template data becomes an injection point.
func TestHTMLBodiesAreStillEscaped(t *testing.T) {
	r := NewTemplateRenderer()

	got, err := r.Render("<p>{{name}}</p>", map[string]any{"name": `<script>alert(1)</script>`}, true)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(got, "<script>") {
		t.Fatalf("HTML body must escape interpolated values, got %q", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("expected escaped output, got %q", got)
	}
}

func TestHTMLEscapesAmpersandsInAttributes(t *testing.T) {
	r := NewTemplateRenderer()
	got, err := r.Render(`<a href="{{url}}">x</a>`, map[string]any{"url": "https://x.test/a?b=1&c=2"}, true)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, "&amp;") {
		t.Errorf("an ampersand in an HTML attribute must be escaped, got %q", got)
	}
}

// The Go-template fallback still has to work for `{{.field}}` templates, which
// raymond cannot parse.
func TestGoTemplateFallbackStillWorks(t *testing.T) {
	r := NewTemplateRenderer()

	text, err := r.Render("Hi {{.Name}}", map[string]any{"Name": "Ada & Co"}, false)
	if err != nil {
		t.Fatalf("text fallback: %v", err)
	}
	if text != "Hi Ada & Co" {
		t.Errorf("text fallback got %q", text)
	}

	html, err := r.Render("<p>{{.Name}}</p>", map[string]any{"Name": "<b>"}, true)
	if err != nil {
		t.Fatalf("html fallback: %v", err)
	}
	if strings.Contains(html, "<b>") {
		t.Errorf("html fallback must escape, got %q", html)
	}
}

// The renderer caches parsed templates in sync.Maps shared across goroutines,
// and the same template string is rendered as both HTML and plain text.
func TestSameTemplateRenderedBothWaysDoesNotLeakEscaping(t *testing.T) {
	r := NewTemplateRenderer()
	const tpl = "{{v}}"
	data := map[string]any{"v": "a & b"}

	if got, _ := r.Render(tpl, data, true); got != "a &amp; b" {
		t.Errorf("html render got %q", got)
	}
	if got, _ := r.Render(tpl, data, false); got != "a & b" {
		t.Errorf("text render after html got %q", got)
	}
	if got, _ := r.Render(tpl, data, true); got != "a &amp; b" {
		t.Errorf("html render after text got %q", got)
	}
}

// markSafeForPlainText must not mutate the caller's map: the same data value is
// passed to three Render calls in a row, one of which is HTML.
func TestMarkSafeDoesNotMutateTheCallersData(t *testing.T) {
	data := map[string]any{
		"a":    "x & y",
		"deep": map[string]any{"b": "p & q"},
		"list": []any{"m & n"},
	}
	_ = markSafeForPlainText(data)

	if _, ok := data["a"].(string); !ok {
		t.Errorf("top-level value was replaced in the caller's map: %T", data["a"])
	}
	deep := data["deep"].(map[string]any)
	if _, ok := deep["b"].(string); !ok {
		t.Errorf("nested value was replaced in the caller's map: %T", deep["b"])
	}
	list := data["list"].([]any)
	if _, ok := list[0].(string); !ok {
		t.Errorf("slice element was replaced in the caller's map: %T", list[0])
	}
}

// The reported case. `created_at` reaches the renderer as a string, because
// protobuf has no JSON date type, and a string has no Format method — so both
// spellings were broken, in opposite and equally bad ways. The dotted one
// failed the template, which fails the send; the dotless one rendered nothing
// and the mail went out with the date missing.
func TestFormatRendersADateField(t *testing.T) {
	r := NewTemplateRenderer()
	data := map[string]any{"created_at": "2026-12-01T09:30:00Z"}

	tests := []struct {
		name string
		tpl  string
		want string
	}{
		{"go spelling", `Sent {{ .created_at.Format "2006-01-02" }}`, "Sent 2026-12-01"},
		{"example date", `Sent {{ .created_at.Format "2026-12-01" }}`, "Sent 2026-12-01"},
		{"single quotes", `Sent {{ .created_at.Format '2026-12-01' }}`, "Sent 2026-12-01"},
		{"no leading dot", `Sent {{created_at.Format '2026-12-01'}}`, "Sent 2026-12-01"},
		{"lowercase method", `Sent {{ .created_at.format "2026-12-01" }}`, "Sent 2026-12-01"},
		{"tokens", `Sent {{ .created_at.Format "DD/MM/YYYY" }}`, "Sent 01/12/2026"},
		{"named", `Sent {{ .created_at.Format "human" }}`, "Sent Dec 1, 2026 9:30 AM"},
		{"trim markers", `Sent {{- .created_at.Format "date" -}} .`, "Sent2026-12-01."},
		{"twice in one template",
			`{{ .created_at.Format "YYYY" }}/{{ .created_at.Format "MM" }}`, "2026/12"},
		{"alongside a plain field",
			`{{ .created_at.Format "date" }} {{ .created_at }}`,
			"2026-12-01 2026-12-01T09:30:00Z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Render(tc.tpl, data, false)
			if err != nil {
				t.Fatalf("Render(%q): %v", tc.tpl, err)
			}
			if got != tc.want {
				t.Errorf("Render(%q) = %q, want %q", tc.tpl, got, tc.want)
			}
		})
	}
}

// A loop resolves its fields against the item, so the date that needs wrapping
// is not one the template text names.
func TestFormatWorksInsideARange(t *testing.T) {
	r := NewTemplateRenderer()
	data := map[string]any{"orders": []any{
		map[string]any{"placed_at": "2026-12-01T09:30:00Z"},
		map[string]any{"placed_at": "2026-12-24T18:00:00Z"},
	}}

	got, err := r.Render(`{{range .orders}}[{{ .placed_at.Format "DD MMM" }}]{{end}}`, data, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "[01 Dec][24 Dec]" {
		t.Errorf("got %q", got)
	}
}

// An HTML body is still an HTML body: routing past raymond must not route past
// the escaping that goes with it.
func TestFormatStillEscapesInHTMLBodies(t *testing.T) {
	r := NewTemplateRenderer()
	data := map[string]any{
		"created_at": "2026-12-01T09:30:00Z",
		"name":       "<script>alert(1)</script>",
	}

	got, err := r.Render(`<p>{{.name}} {{ .created_at.Format "date" }}</p>`, data, true)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(got, "<script>") {
		t.Fatalf("HTML body must escape interpolated values, got %q", got)
	}
	if !strings.Contains(got, "2026-12-01") {
		t.Errorf("expected the formatted date, got %q", got)
	}
}

// Prose is not a method call. A body that mentions ".Format" must keep being
// rendered by Handlebars, which is the only engine that can read `{{name}}`.
func TestProseMentioningFormatDoesNotDivertHandlebars(t *testing.T) {
	r := NewTemplateRenderer()

	got, err := r.Render("Hi {{name}}, ask about the .Format setting", map[string]any{"name": "Ada"}, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "Hi Ada, ask about the .Format setting" {
		t.Errorf("got %q", got)
	}
}

// Rendering three times from one data value is what renderTemplate does for
// every send, and it is where a coercion that wrote back would show up.
func TestFormatRendersTheSameFromReusedData(t *testing.T) {
	r := NewTemplateRenderer()
	data := map[string]any{"created_at": "2026-12-01T09:30:00Z"}
	const tpl = `{{ .created_at.Format "date" }}`

	for i, isHTML := range []bool{false, true, false} {
		got, err := r.Render(tpl, data, isHTML)
		if err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
		if got != "2026-12-01" {
			t.Errorf("render %d got %q", i, got)
		}
	}
}

// A template naming a field that is not a date has to say so. Rendering it as
// empty is how this failed before, and it put mail in front of a recipient with
// the date silently missing.
func TestFormatOnSomethingThatIsNotADateFails(t *testing.T) {
	r := NewTemplateRenderer()

	if _, err := r.Render(`{{ .name.Format "date" }}`, map[string]any{"name": "Ada"}, false); err == nil {
		t.Error("expected an error for .Format on a string that is not a date")
	}
	if _, err := r.Render(`{{ .missing.Format "date" }}`, map[string]any{}, false); err == nil {
		t.Error("expected an error for .Format on a field that is not there")
	}
}

// A template mixing Handlebars blocks with a Go method call cannot be rendered
// by either engine alone. Go cannot parse `{{#each}}`, so such a template keeps
// the behaviour it had before this change rather than starting to fail a send:
// raymond renders it, and the date comes out empty. The two syntaxes are
// documented as alternatives, not as something to mix.
func TestHandlebarsBlocksWithAMethodCallStillRender(t *testing.T) {
	r := NewTemplateRenderer()
	data := map[string]any{"orders": []any{map[string]any{"placed_at": "2026-12-01T09:30:00Z"}}}

	got, err := r.Render(`{{#each orders}}[{{ placed_at.Format "date" }}]{{/each}}`, data, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "[]" {
		t.Errorf("got %q, want the pre-existing empty rendering", got)
	}
}

// The rewrite has to leave every other action alone, including the rune
// constants and single quotes that are legal elsewhere in a template.
func TestRewriteLeavesOtherActionsAlone(t *testing.T) {
	tests := []struct {
		name string
		tpl  string
	}{
		{"handlebars field", "Hi {{name}}"},
		{"go field", "Hi {{.Name}}"},
		{"a field merely named Format", "{{order.Format}}"},
		{"prose", "read about .Format in the docs"},
		{"conditional on a rune", `{{if eq .grade 'a'}}top{{end}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, rewrote := rewriteMethodCalls(tc.tpl)
			if rewrote {
				t.Errorf("template was rewritten to %q", got)
			}
			if got != tc.tpl {
				t.Errorf("template changed: got %q, want %q", got, tc.tpl)
			}
		})
	}
}

// A timestamp is stored as an instant, but a recipient reads it as a wall
// clock. An event mail that names the wrong hour is the failure this prevents.
func TestInRendersATimestampInAnotherZone(t *testing.T) {
	r := NewTemplateRenderer()
	// 09:30 UTC is 16:30 in Jakarta, the same day.
	data := map[string]any{"StartAt": "2026-12-01T09:30:00Z"}

	tests := []struct {
		name string
		tpl  string
		want string
	}{
		{"go longhand, as Go itself spells it",
			`{{ .StartAt.In (time.LoadLocation "Asia/Jakarta") }}`,
			"2026-12-01 16:30:00 +0700 WIB"},
		{"the short spelling",
			`{{ .StartAt.In "Asia/Jakarta" }}`,
			"2026-12-01 16:30:00 +0700 WIB"},
		{"chained with a layout",
			`{{ (.StartAt.In "Asia/Jakarta").Format "2026-12-01 15:04" }}`,
			"2026-12-01 16:30"},
		{"chained with the token dialect",
			`{{ (.StartAt.In "Asia/Jakarta").Format "DD MMM YYYY HH:mm" }}`,
			"01 Dec 2026 16:30"},
		{"a zone as the second argument to Format",
			`{{ .StartAt.Format "2026-12-01 15:04" "Asia/Jakarta" }}`,
			"2026-12-01 16:30"},
		{"no leading dot, and single quotes",
			`{{ StartAt.In 'Asia/Jakarta' }}`,
			"2026-12-01 16:30:00 +0700 WIB"},
		{"a zone west of UTC",
			`{{ .StartAt.Format "2026-12-01 15:04" "America/Los_Angeles" }}`,
			"2026-12-01 01:30"},
		{"UTC is a zone like any other",
			`{{ .StartAt.Format "15:04" "UTC" }}`,
			"09:30"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Render(tc.tpl, data, false)
			if err != nil {
				t.Fatalf("Render(%q): %v", tc.tpl, err)
			}
			if got != tc.want {
				t.Errorf("Render(%q) = %q, want %q", tc.tpl, got, tc.want)
			}
		})
	}
}

// Changing the zone can change the date, not only the hour. An invitation that
// names the wrong day is worse than one that names the wrong hour, because the
// recipient has no reason to doubt it.
func TestInCanMoveTheDate(t *testing.T) {
	r := NewTemplateRenderer()
	// 02:00 UTC on 1 December is still 18:00 on 30 November in Los Angeles.
	data := map[string]any{"StartAt": "2026-12-01T02:00:00Z"}

	got, err := r.Render(`{{ (.StartAt.In "America/Los_Angeles").Format "DDD D MMM, HH:mm" }}`, data, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "Mon 30 Nov, 18:00" {
		t.Errorf("got %q, want the previous day in the target zone", got)
	}
}

// A zone that does not exist has to be named, not quietly rendered as UTC —
// that would put an hour in front of a recipient that nobody chose.
func TestUnknownZoneFailsAndNamesItself(t *testing.T) {
	r := NewTemplateRenderer()
	data := map[string]any{"StartAt": "2026-12-01T09:30:00Z"}

	_, err := r.Render(`{{ .StartAt.In "Asia/Jakata" }}`, data, false)
	if err == nil {
		t.Fatal("expected an error for a misspelled zone")
	}
	if !strings.Contains(err.Error(), "Asia/Jakata") {
		t.Errorf("the error should name the zone, got %q", err)
	}
}

// The zone database has to be there. It is installed in the published image and
// embedded in the binary, and this fails if either stops being true.
func TestTheZoneDatabaseIsAvailable(t *testing.T) {
	for _, zone := range []string{"Asia/Jakarta", "America/Los_Angeles", "Europe/London", "UTC"} {
		if _, err := loadLocation(zone); err != nil {
			t.Errorf("zone %s did not load: %v", zone, err)
		}
	}
}
