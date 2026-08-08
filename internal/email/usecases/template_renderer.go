package usecases

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
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

// Render attempts to render a template using Handlebars (raymond) first,
// and falls back to standard Go templates if Handlebars fails.
// This allows supporting both {{variable}} and {{.variable}} syntax.
func (r *templateRenderer) Render(tpl string, data any, isHTML bool) (string, error) {
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
	if isHTML {
		var goTpl *htmltemplate.Template
		if val, ok := r.goHtmlCache.Load(tpl); ok {
			goTpl = val.(*htmltemplate.Template)
		} else {
			var err error
			goTpl, err = htmltemplate.New("tpl").Parse(tpl)
			if err == nil {
				r.goHtmlCache.Store(tpl, goTpl)
			}
		}

		if goTpl != nil {
			var buf bytes.Buffer
			if err := goTpl.Execute(&buf, data); err == nil {
				return buf.String(), nil
			}
		}
	} else {
		var goTpl *texttemplate.Template
		if val, ok := r.goTextCache.Load(tpl); ok {
			goTpl = val.(*texttemplate.Template)
		} else {
			var err error
			goTpl, err = texttemplate.New("tpl").Parse(tpl)
			if err == nil {
				r.goTextCache.Store(tpl, goTpl)
			}
		}

		if goTpl != nil {
			var buf bytes.Buffer
			if err := goTpl.Execute(&buf, data); err == nil {
				return buf.String(), nil
			}
		}
	}

	return "", fmt.Errorf("failed to render template with both raymond and go templates")
}
