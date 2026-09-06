package stores

import (
	"context"

	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// ShadowWriter records an event somewhere other than the primary store.
//
// Narrow, and takes no error, because the caller must not be able to fail a
// send on account of it. The Postgres writer satisfies this.
type ShadowWriter interface {
	Write(event *entities.EmailEvent)
}

// WithShadow returns a repository that writes every event to both stores and
// reads exclusively from the primary.
//
// This is step two of docs/design/0002-shared-event-store.md, and the whole
// point of it is that it changes no behaviour. Every read still comes from
// Pebble, so the shared table can be compared against it under real traffic
// before anything depends on it — and reverting is removing this wrapper.
//
// Embedding rather than reimplementing: EventRepository has fifteen methods and
// only one of them is being changed. Listing the other fourteen here would mean
// a new method silently bypasses the shadow, which is precisely the class of
// mistake this sequencing exists to avoid.
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
// the shared table that the store everything currently reads from does not
// have, which turns a comparison meant to build confidence into a source of
// false divergence.
func (s *shadowed) Write(ctx context.Context, event *entities.EmailEvent) error {
	if err := s.EventRepository.Write(ctx, event); err != nil {
		return err
	}
	s.shadow.Write(event)
	return nil
}
