package retention

import (
	"context"
	"time"
)

// InboundPruner removes received mail stored before a cutoff.
//
// Identical in shape to LogPruner and deliberately a separate type: these two
// hold the least and the most valuable data panmail keeps, and a policy that
// can be wired to the wrong one by accident is a policy that deletes a
// tenant's mail when it was asked to tidy up debug logs.
type InboundPruner interface {
	TruncateBefore(ctx context.Context, before time.Time) (int64, error)
}
