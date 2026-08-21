package retention

import "time"

// RetentionSetter is a worker that prunes its own store and needs to be told
// how long to keep.
//
// The outbox and webhook queues both prune inside their own send loops, where
// housekeeping is already ordered against live work — the outbox waits until
// the queue is drained before it deletes anything, because a delete scan must
// never delay mail. Retention therefore pushes the configured value to them
// rather than reaching into their tables itself, which also means a change
// saved in the settings page reaches them without a restart.
//
// Implementations are called from the retention worker while their own loop is
// running, so SetRetention must be safe for concurrent use.
type RetentionSetter interface {
	SetRetention(d time.Duration)
}
