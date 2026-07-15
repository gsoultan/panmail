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
		res, err := rayTpl.Exec(data)
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
