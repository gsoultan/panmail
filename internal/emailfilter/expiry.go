package emailfilter

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// ExpirySweeper moves overdue held messages to EXPIRED and announces each one.
//
// It exists to keep two things apart. The retention worker wants a count, so
// it can log "n expired" and move on to the prunes that reclaim disk. The
// webhook subscribers want a message each, because "twelve expired" tells
// nobody which twelve. The repository returns the records and this adapts them
// to the count retention expects.
//
// It also keeps internal/retention from importing this package: retention sees
// only Expire(ctx, now, limit) (int, error), the same signature it always had,
// and PruneBefore, the shape of every other prune it runs.
type ExpirySweeper struct {
	quarantine QuarantineRepository
	notifier   QuarantineNotifier
	outbound   OutboundReleaser
	now        func() time.Time
}

// HeldPruner removes held outbound payloads that no review can still release.
// The outbox's releaser implements it; see PruneBefore.
type HeldPruner interface {
	PruneHeld(ctx context.Context, olderThan time.Time) (int64, error)
}

// NewExpirySweeper wires the quarantine to its subscribers and to the outbox
// that holds the payloads. A nil notifier is a deployment with no webhook
// worker, and expires messages silently. A nil outbound releaser leaves held
// payloads where they are, which only a deployment without outbound filtering
// should do.
func NewExpirySweeper(quarantine QuarantineRepository, notifier QuarantineNotifier, outbound OutboundReleaser) *ExpirySweeper {
	return &ExpirySweeper{quarantine: quarantine, notifier: notifier, outbound: outbound, now: time.Now}
}

// Expire runs one sweep and reports how many messages it expired.
//
// An expired outbound hold's payload is discarded: nothing can release it any
// more, and the outbox row carries the whole request, attachments included.
// Nothing used to remove it. The sweep flipped the review record and left the
// row in the outbox for good, since the outbox's own retention only ever
// prunes FAILED rows.
//
// The notifications go out after the write, one per message, and a message
// with no tenant is skipped rather than broadcast — an event enqueued against
// an empty tenant id would be offered to every subscription the lookup matched.
func (s *ExpirySweeper) Expire(ctx context.Context, now time.Time, limit int) (int, error) {
	expired, err := s.quarantine.Expire(ctx, now, limit)
	if err != nil {
		return 0, err
	}

	for i := range expired {
		s.discard(ctx, &expired[i])
	}

	if s.notifier == nil {
		return len(expired), nil
	}

	decidedAt := s.now().UTC()
	for i := range expired {
		record := &expired[i]
		if record.TenantID == "" {
			continue
		}
		// decidedAt from the sweep's own clock, because nothing stamped
		// ReviewedAt: an expiry is the absence of a review, which is the whole
		// reason it is a separate event from a rejection.
		s.notifier.NotifyExpired(record.TenantID, outcomeEventFor(record, decidedAt))
	}
	return len(expired), nil
}

// discard drops an expired outbound hold's payload. A failure is logged and
// left to PruneBefore, which removes any held payload whose review is over:
// the expiry has been recorded and announced, and it stands either way.
func (s *ExpirySweeper) discard(ctx context.Context, record *FilteredMessage) {
	if s.outbound == nil || record.Direction != DirectionOutbound ||
		record.Action != ActionHold || record.PayloadRef == "" {
		return
	}
	if err := s.outbound.DiscardHeld(ctx, record.TenantID, record.PayloadRef); err != nil {
		slog.Error("could not discard the payload of an expired message; the retention prune will",
			"error", err, "id", record.ID, "message_id", record.MessageID)
	}
}

// PruneBefore deletes review history decided before the cutoff, and the held
// payloads no review can release any more. Retention runs it with the
// quarantine policy's cutoff, so a record is kept for one quarantine period
// after its decision and a policy of zero keeps everything.
//
// Two things, both of which used to grow for the life of a deployment:
//
//   - filtered_messages rows. Nothing ever deleted one, decided or not.
//   - HELD outbox rows whose payload was never discarded: a reject whose
//     discard failed, an expiry from before the sweep discarded anything, a
//     hold whose review record was never written. The pruner never touches a
//     row a reviewer can still release, whatever its age.
func (s *ExpirySweeper) PruneBefore(ctx context.Context, before time.Time) (int64, error) {
	removed, err := s.quarantine.PruneTerminal(ctx, before)

	if pruner, ok := s.outbound.(HeldPruner); ok {
		held, heldErr := pruner.PruneHeld(ctx, before)
		removed += held
		err = errors.Join(err, heldErr)
	}
	return removed, err
}
