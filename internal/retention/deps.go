package retention

import "time"

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

// Deps are the stores retention acts on. Every field is optional: a nil store
// is one this deployment does not have, and its policy is skipped rather than
// panicking a background worker.
type Deps struct {
	Events   EventPruner
	Logs     LogPruner
	Inbound  InboundPruner
	Outbox   RetentionSetter
	Webhooks RetentionSetter

	// Interval between passes. Zero means DefaultInterval.
	Interval time.Duration

	// PassTimeout bounds one pass. Zero means DefaultPassTimeout.
	PassTimeout time.Duration

	// StartupDelay holds the first pass back after Start. Zero means
	// DefaultStartupDelay; negative runs it immediately, which is what the
	// tests want and no deployment does.
	StartupDelay time.Duration
}
