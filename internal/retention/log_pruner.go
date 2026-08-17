package retention

import (
	"context"
	"time"
)

// LogPruner removes application log entries written before a cutoff.
type LogPruner interface {
	TruncateBefore(ctx context.Context, before time.Time) (int64, error)
}
