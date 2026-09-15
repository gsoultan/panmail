package usecases

import (
	"bytes"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"regexp"
	"strconv"
	"strings"
	"sync"
	texttemplate "text/template"

	"github.com/aymerick/raymond"
)

// TemplateRenderer defines the interface for rendering email templates.
type TemplateRenderer interface {
	Render(tpl string, data any, isHTML bool) (string, error)
}

type templateRenderer struct {
	raymondCache sync.Map
	goHtmlCache  sync.Map
	goTextCache  sync.Map
	// rewriteCache holds the Go-template source for templates that call a
	// method, so the rewrite runs once per template rather than once per mail.
	rewriteCache sync.Map
}

// NewTemplateRenderer creates a new instance of TemplateRenderer.
func NewTemplateRenderer() TemplateRenderer {
	return &templateRenderer{}
}

// markSafeForPlainText wraps every string in the data so Handlebars does not
// HTML-escape it.
//
// Handlebars escapes `{{x}}` unconditionally, and raymond offers no way to turn
// that off for a render — only `SafeString`, which it checks on the evaluated
// value. Without this, a plain-text body or a subject line rendered from
// `{{company}}` where company is "Tom & Jerry" reaches the recipient as
// "Tom &amp; Jerry". Subjects are the worst case: they are always rendered as
// plain text, and there is no client that will decode entities in one.
//
// The alternative, unescaping the rendered output, would also decode entities
// the template author typed literally. This escapes nothing in the first place.
//
// Template data arrives from structpb.AsMap, so the value shapes are exactly
// string, float64, bool, nil, []any and map[string]any; the string-keyed and
// string-element cases are handled too for callers that build data by hand.
func markSafeForPlainText(value any) any {
	switch v := value.(type) {
	case string:
		return raymond.SafeString(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = markSafeForPlainText(item)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = raymond.SafeString(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = markSafeForPlainText(item)
		}
		return out
	case []string:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = raymond.SafeString(item)
		}
		return out
	default:
		// Numbers, booleans and nil have no characters worth escaping.
		return value
	}
}

// actionRe matches one `{{ ... }}` action, which is the only place a method
// call counts. A body that mentions ".Format" in its prose must not divert a
// Handlebars template to the wrong engine.
var actionRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// quotedArg is a layout or a zone name, in any of the three quotes either
// engine accepts, or a reference to a field holding one.
const quotedArg = "(\"[^\"]*\"|'[^']*'|`[^`]*`|\\$[\\w.]+|\\.[\\w.]+)"

// pathArg is the value the method is called on.
const pathArg = "(\\$?[A-Za-z_.][\\w.]*)"

// formatCallRe matches `created_at.Format "2026-12-01"` — a path, the method,
// its layout, and optionally a zone to read the timestamp in.
//
// Requiring the layout is what separates a method call from a Handlebars path:
// `{{order.Format}}` is a field that happens to be named Format and still
// belongs to raymond, while `{{order.placed_at.Format "date"}}` is something
// Handlebars cannot express at all. The path may be written with or without its
// leading dot.
var formatCallRe = regexp.MustCompile(
	pathArg + "\\.[Ff]ormat[ \t]+" + quotedArg + "(?:[ \t]+" + quotedArg + ")?")

// inCallRe matches `start_at.In "Asia/Jakarta"` and the longhand Go spelling
// `start_at.In (time.LoadLocation "Asia/Jakarta")`.
//
// The longhand has to be recognised here, in the template text, because it can
// never reach Go's parser: a template function name must be a Go identifier, so
// nothing can be registered under the name "time.LoadLocation".
var inCallRe = regexp.MustCompile(
	pathArg + "\\.In[ \t]+(?:\\([ \t]*time\\.LoadLocation[ \t]+" + quotedArg +
		"[ \t]*\\)|" + quotedArg + ")")

// rewriteMethodCalls turns every method call into a call to a registered
// function, and reports whether it found one.
//
// Rewriting rather than relying on Go's own method dispatch is what makes a
// missing field an error instead of the text "<no value>" in a sent mail, and
// it accepts the spellings an author actually writes: with or without the
// leading dot, and with the argument in single quotes as Handlebars allows.
//
// `.In` is rewritten before `.Format` so that the two compose. After the first
// pass `(.start_at.In "Asia/Jakarta").Format "date"` reads
// `(inZone .start_at "Asia/Jakarta").Format "date"`, where `.Format` is no
// longer preceded by a path and so is left as what it is: a method call on the
// value the first function returned.
func rewriteMethodCalls(tpl string) (string, bool) {
	rewrote := false

	out := actionRe.ReplaceAllStringFunc(tpl, func(action string) string {
		if !containsMethodCall(action) {
			return action
		}

		action = inCallRe.ReplaceAllStringFunc(action, func(call string) string {
			parts := inCallRe.FindStringSubmatch(call)
			if parts == nil {
				return call
			}
			rewrote = true
			// Group 2 is the longhand's zone, group 3 the bare one; exactly one
			// of them matched.
			zone := parts[2]
			if zone == "" {
				zone = parts[3]
			}
			return "inZone " + goPath(parts[1]) + " " + goStringArg(zone)
		})

		return formatCallRe.ReplaceAllStringFunc(action, func(call string) string {
			parts := formatCallRe.FindStringSubmatch(call)
			if parts == nil {
				return call
			}
			rewrote = true
			out := "formatDate " + goPath(parts[1]) + " " + goStringArg(parts[2])
			if parts[3] != "" {
				out += " " + goStringArg(parts[3])
			}
			return out
		})
	})

	return out, rewrote
}

// goPath gives a field reference the leading dot Go requires. Without it
// `created_at` reads as the name of a function that does not exist.
func goPath(path string) string {
	if strings.HasPrefix(path, ".") || strings.HasPrefix(path, "$") {
		return path
	}
	return "." + path
}

// goStringArg rewrites a layout written in single quotes or backticks. Go reads
// single quotes as a rune constant and rejects anything longer than one
// character, so `.Format '2026-12-01'` did not parse at all.
func goStringArg(arg string) string {
	if strings.HasPrefix(arg, "'") || strings.HasPrefix(arg, "`") {
		return strconv.Quote(arg[1 : len(arg)-1])
	}
	return arg
}

// containsMethodCall is the cheap test that keeps this off the hot path: every
// template rendered checks for a method call, and all but the ones using it pay
// only a substring scan. A false positive costs one regex on a cache miss; the
// regexes above are what actually decide.
func containsMethodCall(tpl string) bool {
	return strings.Contains(tpl, ".Format") ||
		strings.Contains(tpl, ".format") ||
		strings.Contains(tpl, ".In")
}

// prepare returns the Go-template source for a template that calls a method,
// and reports false for every other template.
func (r *templateRenderer) prepare(tpl string) (string, bool) {
	if val, ok := r.rewriteCache.Load(tpl); ok {
		rewritten := val.(string)
		return rewritten, rewritten != ""
	}

	rewritten, ok := rewriteMethodCalls(tpl)
	if !ok {
		rewritten = ""
	}
	r.rewriteCache.Store(tpl, rewritten)
	return rewritten, ok
}

// templateFuncs are the functions a rewritten template calls. They are
// registered on every Go template, not only rewritten ones, so that the cache
// holds one kind of template and not two.
var templateFuncs = map[string]any{"formatDate": formatDate, "inZone": inZone}

// parseError marks a template Go's engine could not parse, as opposed to one
// it parsed and then failed to execute. The two mean opposite things for a
// method call: the first says the template is not Go syntax at all, the second
// says the method call itself is wrong.
type parseError struct{ err error }

func (e parseError) Error() string { return e.err.Error() }

func (e parseError) Unwrap() error { return e.err }

// renderGo renders with Go's template engine, caching the parsed template.
//
// Keyed by the source actually parsed rather than by the template as written,
// so a rewritten method-call template and a template taking the Handlebars
// fallback cannot collide on one cache entry.
func (r *templateRenderer) renderGo(source string, data any, isHTML bool) (string, error) {
	var buf bytes.Buffer

	if isHTML {
		goTpl, ok := r.goHtmlCache.Load(source)
		if !ok {
			parsed, err := htmltemplate.New("tpl").Funcs(templateFuncs).Parse(source)
			if err != nil {
				return "", parseError{err}
			}
			goTpl, _ = r.goHtmlCache.LoadOrStore(source, parsed)
		}
		if err := goTpl.(*htmltemplate.Template).Execute(&buf, data); err != nil {
			return "", err
		}
		return buf.String(), nil
	}

	goTpl, ok := r.goTextCache.Load(source)
	if !ok {
		parsed, err := texttemplate.New("tpl").Funcs(templateFuncs).Parse(source)
		if err != nil {
			return "", parseError{err}
		}
		goTpl, _ = r.goTextCache.LoadOrStore(source, parsed)
	}
	if err := goTpl.(*texttemplate.Template).Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Render renders a template with Handlebars (raymond) first, falling back to
// standard Go templates if Handlebars fails. This allows supporting both
// {{variable}} and {{.variable}} syntax.
//
// The one exception is a template that calls a method, such as
// `{{ created_at.Format "2026-12-01" }}`, which goes to Go's engine first —
// see below for why raymond cannot be allowed to try that one.
func (r *templateRenderer) Render(tpl string, data any, isHTML bool) (string, error) {
	// 0. A template that calls a method cannot be Handlebars, which has no
	// method calls — and raymond does not report that as an error. It resolves
	// `created_at.Format` as a path it cannot find and renders nothing at all,
	// so the send succeeds with the date missing from the mail. A wrong date is
	// worse than a failed render, so these templates skip raymond entirely and
	// are rendered by Go, which is the engine whose syntax they are written in.
	var methodErr error
	if containsMethodCall(tpl) {
		if source, ok := r.prepare(tpl); ok {
			out, err := r.renderGo(source, data, isHTML)
			if err == nil {
				return out, nil
			}

			var notGoSyntax parseError
			if !errors.As(err, &notGoSyntax) {
				// The template is Go syntax and the method call is what failed:
				// the field is absent, or holds something that is not a date.
				// Handing it to raymond would render the date as nothing at all
				// and send the mail anyway, which is how this bug looked before.
				return "", fmt.Errorf("failed to render template: %w", err)
			}
			// Not a Go template — Handlebars block syntax, most likely. Let the
			// engine that can parse it try, and keep this reason in case it
			// cannot either.
			methodErr = err
		}
	}

	// 1. Try Handlebars (raymond) - Primary engine for this project
	var rayTpl *raymond.Template
	if val, ok := r.raymondCache.Load(tpl); ok {
		rayTpl = val.(*raymond.Template)
	} else {
		var err error
		rayTpl, err = raymond.Parse(tpl)
		if err == nil {
			r.raymondCache.Store(tpl, rayTpl)
		}
	}

	if rayTpl != nil {
		// isHTML has to be honoured here, not only in the Go-template fallback
		// below. raymond succeeds for almost every template, so the fallback is
		// rarely reached and the flag was in practice never consulted.
		payload := data
		if !isHTML {
			payload = markSafeForPlainText(data)
		}
		res, err := rayTpl.Exec(payload)
		if err == nil {
			return res, nil
		}
	}

	// 2. If Handlebars fails, try Go templates as a fallback.
	if out, err := r.renderGo(tpl, data, isHTML); err == nil {
		return out, nil
	}

	if methodErr != nil {
		return "", fmt.Errorf("failed to render template: %w", methodErr)
	}
	return "", fmt.Errorf("failed to render template with both raymond and go templates")
}
