package retention

import (
	"context"
	"time"

	"github.com/gsoultan/panmail/internal/system_settings/entities"
)

// DefaultInterval is how often retention runs when no interval is given.
// Retention is measured in days, so a daily pass is as precise as the policy
// it enforces, and every pass is a full scan of stores that exist to serve
// live traffic.
const DefaultInterval = 24 * time.Hour

// DefaultPassTimeout bounds a single pass. Long enough that a full scan and
// compaction of a store years deep finishes, short enough that a wedged one
// gives the next pass a turn rather than stopping retention for good.
const DefaultPassTimeout = time.Hour

// DefaultStartupDelay holds the first pass back after boot.
//
// A pass scans every store and compacts what it deleted, which is the last
// thing a process should be doing while it is still opening databases and
// taking its first requests. Short, though, and not skipped: a gateway that is
// restarted daily would otherwise never reach its first tick, and retention
// would silently never run at all.
const DefaultStartupDelay = time.Minute

// SettingsSource reads the stored settings the policy is resolved from.
//
// Narrow on purpose: retention needs to read one row and must not be able to
// write it. system_settings/repositories.SettingsRepository satisfies this as
// written.
type SettingsSource interface {
	Get(ctx context.Context) (*entities.Settings, error)
}

// Deps are the stores retention acts on. Every field is optional: a nil store
// is one this deployment does not have, and its policy is skipped rather than
// panicking a background worker.
// QuarantineExpirer moves held messages nobody reviewed out of PENDING.
//
// Not a pruner and not days-based: the expiry is stamped on each row when the
// message is held, from the filter's own retention, so this asks "what is past
// its own deadline" rather than "what is older than N days". Nothing is
// deleted — the row becomes EXPIRED, which is a different fact from rejected
// and worth being able to count.
type QuarantineExpirer interface {
	Expire(ctx context.Context, now time.Time, limit int) (int, error)
}

type Deps struct {
	// Settings is where the policy is read from, once per pass. Unlike every
	// other field it is required: a worker that cannot read the policy has no
	// basis for deleting anything, and guessing at one means deleting on
	// defaults a deployment never agreed to.
	Settings SettingsSource

	Events   EventPruner
	Logs     LogPruner
	Inbound  InboundPruner
	Outbox   RetentionSetter
	Webhooks RetentionSetter

	// Quarantine is optional; without it nothing expires.
	Quarantine QuarantineExpirer

	// Screener receives the quarantine retention the same way the outbox and
	// webhook queues receive theirs, which is what makes a change on the
	// settings page apply without a restart.
	Screener RetentionSetter

	// Interval between passes. Zero means DefaultInterval.
	Interval time.Duration

	// PassTimeout bounds one pass. Zero means DefaultPassTimeout.
	PassTimeout time.Duration

	// StartupDelay holds the first pass back after Start. Zero means
	// DefaultStartupDelay; negative runs it immediately, which is what the
	// tests want and no deployment does.
	StartupDelay time.Duration
}
