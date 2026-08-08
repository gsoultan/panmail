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
