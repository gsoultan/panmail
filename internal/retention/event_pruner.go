package retention

import (
	"context"
	"time"
)

// EventPruner is the part of the event store that retention drives: delivery
// events, the message bodies stored alongside them, and the JSONL archives
// written when events expire.
//
// The three are separate methods because they are separate policies. Bodies
// carry the recipient's actual mail and attachments, so an operator commonly
// wants them gone long before the delivery history that refers to them; the
// archives are the escape hatch for the events and outlive both.
type EventPruner interface {
	TruncateBefore(ctx context.Context, before time.Time) (int64, error)
	TruncateMessagesBefore(ctx context.Context, before time.Time) (int64, error)
	PruneArchivesBefore(ctx context.Context, before time.Time) (int64, error)
}

// TelemetryPruner is the part of the event store that keeps derived,
// high-volume telemetry: per-minute event counters and resource samples.
//
// Optional, and asserted rather than added to EventPruner, so a store without
// these has nothing to implement.
type TelemetryPruner interface {
	PruneMinuteSeriesBefore(ctx context.Context, before time.Time) (int64, error)
	PruneResourceHistoryBefore(ctx context.Context, before time.Time) (int64, error)
}

// The horizons for the telemetry classes. Fixed rather than configurable,
// because nobody chooses to keep these: they are written for the life of the
// process whatever the policy says, and read only over a short window.
const (
	// MinuteSeriesDays keeps per-minute counters for a month. The dashboard
	// asks for minute buckets over six hours at most, and a ranged total
	// reads them only for the ragged minutes at its edges. Day and hour
	// counters are not affected; they are kept for good.
	MinuteSeriesDays = 30

	// ResourceHistoryDays keeps a week of resource samples. The performance
	// page reads the last 24 hours.
	ResourceHistoryDays = 7
)
