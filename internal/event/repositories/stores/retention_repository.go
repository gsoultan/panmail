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
}
