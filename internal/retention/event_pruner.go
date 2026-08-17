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
