package retention

import "time"

// Stats is what retention has done since the process started.
//
// The number that matters is LastRun. Everything here is a background pass
// nobody watches, and the failure that costs an operator their disk is not a
// pass that deleted the wrong thing — it is a pass that silently stopped
// happening, which no amount of removal counting will show.
type Stats struct {
	Removed  int64
	Failures int64

	// When the last pass completed. Zero if none has: see SinceLastRun for why
	// that is not the same as "recently".
	LastRun time.Time
}

// SinceLastRun is how long retention has gone without completing a pass.
//
// Before the first pass this measures from process start rather than
// returning zero, so an alert on "retention has not run in 48 hours" fires for
// a gateway where it never ran at all — the case a zero would hide.
func (w *Worker) SinceLastRun() time.Duration {
	last := w.lastRun.Load()
	if last == 0 {
		return w.now().Sub(w.startedAt)
	}
	return w.now().Sub(time.Unix(0, last))
}

// Stats reports the counters behind the retention metrics.
func (w *Worker) Stats() Stats {
	stats := Stats{
		Removed:  w.removed.Load(),
		Failures: w.failures.Load(),
	}
	if last := w.lastRun.Load(); last != 0 {
		stats.LastRun = time.Unix(0, last)
	}
	return stats
}
