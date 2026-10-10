package stores

import (
	"context"
	"time"
)

// RetentionRepository is the part of the event store that expires data:
// delivery events, the message bodies stored alongside them, and the JSONL
// archives written when events expire.
//
// It is stated here, rather than imported from internal/retention, so that the
// repository layer keeps declaring what it provides and the policy layer keeps
// declaring what it needs. Go matches the two structurally, and neither
// package has to know the other exists.
//
// Each method returns how much it removed, because "cleanup successful" with
// no number tells an operator nothing about whether their policy is doing
// anything.
type RetentionRepository interface {
	TruncateBefore(ctx context.Context, before time.Time) (int64, error)
	TruncateMessagesBefore(ctx context.Context, before time.Time) (int64, error)
	PruneArchivesBefore(ctx context.Context, before time.Time) (int64, error)

	// The two classes with a fixed horizon rather than a policy: per-minute
	// counters and resource samples. Both are written for the life of the
	// process, and neither is read beyond a short window, so nobody is asked
	// how long to keep them.
	PruneMinuteSeriesBefore(ctx context.Context, before time.Time) (int64, error)
	PruneResourceHistoryBefore(ctx context.Context, before time.Time) (int64, error)

	// Day and hour counters, which a long chart is drawn from. Kept forever
	// unless the analytics retention is set, and then pruned on it.
	PruneHourDaySeriesBefore(ctx context.Context, before time.Time) (int64, error)
}

// ArchivedElsewhere reports which of the given event ids another store holds
// and will archive when they expire there.
type ArchivedElsewhere func(ctx context.Context, ids []string) (map[string]bool, error)

// SharedPruner is the shared store's retention over its own tables alone: no
// archive written and no local store touched.
//
// It is what the shadow phase needs. There the local store is the primary and
// archives everything it expires, so the database copy has only to be deleted
// — archiving it as well would write every expired event twice, and pruning
// through the shared store's full retention would prune the primary a second
// time.
type SharedPruner interface {
	DeleteEventsBefore(ctx context.Context, before time.Time) (int64, error)
	DeleteMessagesBefore(ctx context.Context, before time.Time) (int64, error)
	DeleteMinuteSeriesBefore(ctx context.Context, before time.Time) (int64, error)
	DeleteHourDaySeriesBefore(ctx context.Context, before time.Time) (int64, error)
}
