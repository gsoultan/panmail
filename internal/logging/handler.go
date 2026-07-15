package logging

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

type PebbleHandler struct {
	store Store
	level slog.Leveler
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
	metadata := make(map[string]string)
	record.Attrs(func(attr slog.Attr) bool {
		metadata[attr.Key] = attr.Value.String()
		return true
	})

	tenantID := ""
	if ctx != nil {
		if val := ctx.Value("tenant_id"); val != nil {
			if tid, ok := val.(string); ok {
				tenantID = tid
			}
		}
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

func (h *PebbleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// For simplicity, not implementing full structured logging with persistent attributes
	return h
}

func (h *PebbleHandler) WithGroup(name string) slog.Handler {
	return h
}
