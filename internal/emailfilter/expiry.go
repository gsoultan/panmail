package emailfilter

import (
	"context"
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
// only Expire(ctx, now, limit) (int, error), the same signature it always had.
type ExpirySweeper struct {
	quarantine QuarantineRepository
	notifier   QuarantineNotifier
	now        func() time.Time
}

// NewExpirySweeper wires the quarantine to its subscribers. A nil notifier is
// a deployment with no webhook worker, and expires messages silently.
func NewExpirySweeper(quarantine QuarantineRepository, notifier QuarantineNotifier) *ExpirySweeper {
	return &ExpirySweeper{quarantine: quarantine, notifier: notifier, now: time.Now}
}

// Expire runs one sweep and reports how many messages it expired.
//
// The notifications go out after the write, one per message, and a message
// with no tenant is skipped rather than broadcast — an event enqueued against
// an empty tenant id would be offered to every subscription the lookup matched.
func (s *ExpirySweeper) Expire(ctx context.Context, now time.Time, limit int) (int, error) {
	expired, err := s.quarantine.Expire(ctx, now, limit)
	if err != nil {
		return 0, err
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
