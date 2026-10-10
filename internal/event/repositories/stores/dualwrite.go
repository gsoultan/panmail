package stores

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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

// TruncateBefore expires events in the primary, which archives them, and then
// deletes the shadow's copies of the same events.
//
// The shadow's rows used to be left alone, because the embedded primary
// answered retention by itself — so the shared tables grew for the whole of the
// shadow phase, which can be long. They are deleted without archiving: every
// shadowed event is also in the primary (it is shadowed only after the primary
// took it), so the primary's archive already has it, and a second would put
// each expired event in the archive twice.
//
// The count is the primary's. The shadow's is the same events again.
func (s *shadowed) TruncateBefore(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.EventRepository.TruncateBefore(ctx, before)
	if err != nil {
		return n, err
	}
	return n, s.pruneShadow(func(p SharedPruner) (int64, error) {
		return p.DeleteEventsBefore(ctx, before)
	})
}

// TruncateMessagesBefore deletes expired bodies from both stores.
func (s *shadowed) TruncateMessagesBefore(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.EventRepository.TruncateMessagesBefore(ctx, before)
	if err != nil {
		return n, err
	}
	return n, s.pruneShadow(func(p SharedPruner) (int64, error) {
		return p.DeleteMessagesBefore(ctx, before)
	})
}

// PruneMinuteSeriesBefore deletes expired per-minute counters from both
// stores, since the shadow keeps its own.
func (s *shadowed) PruneMinuteSeriesBefore(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.EventRepository.PruneMinuteSeriesBefore(ctx, before)
	if err != nil {
		return n, err
	}
	return n, s.pruneShadow(func(p SharedPruner) (int64, error) {
		return p.DeleteMinuteSeriesBefore(ctx, before)
	})
}

// pruneShadow runs one prune against the shadow's own tables, if it has any.
//
// Its failure is returned, unlike a failed shadow write. A write the shadow
// missed is a gap in a comparison; a prune it keeps missing is a table that
// grows until the disk is full, and the retention failure metric is the only
// place that would ever show it.
func (s *shadowed) pruneShadow(prune func(SharedPruner) (int64, error)) error {
	p, ok := s.shadow.(SharedPruner)
	if !ok {
		return nil
	}
	if _, err := prune(p); err != nil {
		return fmt.Errorf("shadow store: %w", err)
	}
	return nil
}

// Close flushes the shadow's writer and then closes the primary.
//
// The embedded primary's Close used to answer alone, so the shadow's batching
// writer was never stopped: it ended with the worker context and took any
// event recorded after that — an outbox send finishing during shutdown — with
// it. The shadow itself is not closed, because the local store it would close
// is the primary.
func (s *shadowed) Close() error {
	if w, ok := s.shadow.(interface{ StopWriter() }); ok {
		w.StopWriter()
	}
	return s.EventRepository.Close()
}

// BackfillMessageProvider records a message's provider in the primary and then
// the shadow, on the same terms as the writes: the shadow's failure is logged,
// not returned.
func (s *shadowed) BackfillMessageProvider(ctx context.Context, tenantID, messageID, providerID string) error {
	if b, ok := s.EventRepository.(MessageProviderBackfiller); ok {
		if err := b.BackfillMessageProvider(ctx, tenantID, messageID, providerID); err != nil {
			return err
		}
	} else {
		// A primary that cannot set the field alone gets the read and
		// rewrite, which also reaches the shadow through WriteMessage.
		m, err := s.EventRepository.GetMessage(ctx, tenantID, messageID)
		if err != nil || m == nil || m.ProviderID != "" {
			return err
		}
		m.ProviderID = providerID
		return s.WriteMessage(ctx, m)
	}

	if b, ok := s.shadow.(MessageProviderBackfiller); ok {
		if err := b.BackfillMessageProvider(ctx, tenantID, messageID, providerID); err != nil {
			slog.Warn("could not shadow a message's provider", "error", err, "id", messageID)
		}
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
