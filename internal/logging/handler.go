package logging

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
)

// tenantAttr is the attribute a log call names its tenant with, as in
// slog.Info("...", "tenant_id", id). Almost no log call has a request context
// to hand, so the attribute is how an entry finds its way to a tenant's view.
const tenantAttr = "tenant_id"

type PebbleHandler struct {
	store Store
	level slog.Leveler
	attrs []slog.Attr
	group string
}

func NewPebbleHandler(store Store, level slog.Leveler) *PebbleHandler {
	return &PebbleHandler{
		store: store,
		level: level,
	}
}

func (h *PebbleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *PebbleHandler) Handle(ctx context.Context, record slog.Record) error {
	metadata := make(map[string]string, len(h.attrs)+record.NumAttrs())
	for _, attr := range h.attrs {
		metadata[h.key(attr.Key)] = attr.Value.String()
	}
	record.Attrs(func(attr slog.Attr) bool {
		metadata[h.key(attr.Key)] = attr.Value.String()
		return true
	})

	// The authenticated tenant wins over an attribute. The context lookup
	// must use the auth package's typed key: a plain "tenant_id" string key
	// never matches it, which is how every entry used to be stored with no
	// tenant and so shown to every tenant's administrator.
	tenantID := ""
	if ctx != nil {
		tenantID = middlewares.GetTenantID(ctx)
	}
	if tenantID == "" {
		tenantID = metadata[tenantAttr]
	}

	entry := LogEntry{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Timestamp: record.Time,
		Level:     record.Level.String(),
		Message:   record.Message,
		Metadata:  metadata,
	}

	return h.store.Write(entry)
}

func (h *PebbleHandler) key(k string) string {
	if h.group == "" {
		return k
	}
	return h.group + "." + k
}

// WithAttrs keeps the attributes, so a logger built with
// slog.With("tenant_id", id) still records them. Returning the receiver, as
// this once did, silently dropped every one.
func (h *PebbleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	clone := *h
	clone.attrs = make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	clone.attrs = append(clone.attrs, h.attrs...)
	for _, a := range attrs {
		a.Key = h.key(a.Key)
		clone.attrs = append(clone.attrs, a)
	}
	clone.group = h.group
	return &clone
}

func (h *PebbleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.group = h.key(name)
	return &clone
}
