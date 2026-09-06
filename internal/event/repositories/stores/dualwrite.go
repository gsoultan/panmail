package stores

import (
	"context"
	"log/slog"

	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// ShadowWriter records to a second store alongside the primary.
//
// Both halves are here rather than events alone, and that was learned rather
// than designed: shadowing only events meant the shared store had every
// timeline and no bodies, so switching reads to it produced a working dashboard
// with a blank Content tab for everything written before the switch. A shadow
// that covers part of what a reader needs is not a shadow.
type ShadowWriter interface {
	Write(ctx context.Context, event *entities.EmailEvent) error
	WriteMessage(ctx context.Context, message *entities.EmailMessage) error
}

// WithShadow returns a repository that writes to both stores and reads
// exclusively from the primary.
//
// This is step two of docs/design/0002-shared-event-store.md, and the whole
// point of it is that it changes no behaviour. Every read still comes from
// Pebble, so the shared store can be compared against it under real traffic
// before anything depends on it — and reverting is removing this wrapper.
//
// Embedding rather than reimplementing: EventRepository has fifteen methods and
// only two are being changed. Listing the other thirteen would mean a new
// method silently bypasses the shadow, which is the class of mistake this
// sequencing exists to avoid.
func WithShadow(primary EventRepository, shadow ShadowWriter) EventRepository {
	if primary == nil || shadow == nil {
		return primary
	}
	return &shadowed{EventRepository: primary, shadow: shadow}
}

type shadowed struct {
	EventRepository
	shadow ShadowWriter
}

// Write records to the primary first and shadows it only on success.
//
// The order matters. Shadowing a write the primary rejected would put a row in
// the shared store that the one everything reads from does not have, which
// turns a comparison meant to build confidence into a source of false
// divergence.
func (s *shadowed) Write(ctx context.Context, event *entities.EmailEvent) error {
	if err := s.EventRepository.Write(ctx, event); err != nil {
		return err
	}
	// The shadow's outcome is deliberately not returned. Nothing reads it yet,
	// and failing a send because a store nobody queries was busy would be the
	// wrong trade — the whole point of the phase is that it cannot hurt.
	if err := s.shadow.Write(ctx, event); err != nil {
		slog.Warn("could not shadow a delivery event", "error", err, "id", event.ID)
	}
	return nil
}

// WriteMessage shadows the stored body on the same terms.
func (s *shadowed) WriteMessage(ctx context.Context, message *entities.EmailMessage) error {
	if err := s.EventRepository.WriteMessage(ctx, message); err != nil {
		return err
	}
	if err := s.shadow.WriteMessage(ctx, message); err != nil {
		slog.Warn("could not shadow a stored message", "error", err, "id", message.ID)
	}
	return nil
}
