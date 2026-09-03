package retention

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/gsoultan/panmail/internal/system_settings/entities"
)

const (
	classEvents   = "events"
	classMessages = "messages"
	classArchives = "archives"
	classAppLogs  = "app_logs"
	classInbound  = "inbound"
)

// Worker enforces the retention policy on a schedule.
//
// One worker for every class, rather than one per store: the policy is a
// single document an administrator edits in one place, and pruning spread
// across seven independent loops is pruning nobody can reason about. It
// re-reads the configuration on every pass, so a change saved in the settings
// page applies without a restart, and Trigger makes that pass happen at once
// instead of at the next daily tick.
type Worker struct {
	deps    Deps
	trigger chan struct{}
	now     func() time.Time

	// Where the policy comes from on every pass. It reads the database rather
	// than a file so that a change saved on one instance is enforced by all of
	// them; see the package comment on system_settings/entities.
	load func(context.Context) (*entities.Settings, error)

	// Read by the metrics callbacks on whatever goroutine scrapes, written by
	// the pass. See stats.go for what they are for.
	removed   atomic.Int64
	failures  atomic.Int64
	lastRun   atomic.Int64 // unix nanos; zero until a pass completes
	startedAt time.Time
}

func NewWorker(deps Deps) *Worker {
	if deps.Interval <= 0 {
		deps.Interval = DefaultInterval
	}
	if deps.PassTimeout <= 0 {
		deps.PassTimeout = DefaultPassTimeout
	}
	if deps.StartupDelay == 0 {
		deps.StartupDelay = DefaultStartupDelay
	}
	w := &Worker{
		deps:      deps,
		trigger:   make(chan struct{}, 1),
		now:       time.Now,
		startedAt: time.Now(),
	}

	// Guarded rather than taken directly: a method value off a nil interface
	// panics where it is written, which would be inside this constructor and
	// so before any recover a worker is wrapped in. A missing source has to
	// read as "policy unavailable" — RunOnce then logs and prunes nothing,
	// which is the right answer to not knowing the policy.
	if deps.Settings != nil {
		w.load = deps.Settings.Get
	} else {
		w.load = func(context.Context) (*entities.Settings, error) {
			return nil, errors.New("retention has no settings source")
		}
	}
	return w
}

// Trigger asks for a pass now. A request already pending is enough — the
// buffered slot means "run again", so a burst of saves does not queue a burst
// of full-store scans.
func (w *Worker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

func (w *Worker) Start(ctx context.Context) {
	slog.Info("retention worker started",
		"interval", w.deps.Interval, "first_pass_in", w.deps.StartupDelay)
	defer slog.Info("retention worker stopped")

	ticker := time.NewTicker(w.deps.Interval)
	defer ticker.Stop()

	// The first pass waits, so that a scan of every store does not run while
	// the process is still opening databases and taking its first requests. A
	// Trigger from the settings page still cuts the wait short.
	startup := time.NewTimer(w.deps.StartupDelay)
	defer startup.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-startup.C:
		case <-ticker.C:
		case <-w.trigger:
		}
		w.runPass(ctx)
	}
}

// runPass bounds one pass. Passes never overlap — the loop is sequential — so
// the timeout is not about concurrency: it is so that a scan wedged on a store
// that will not answer eventually gives up and lets the next one try, instead
// of retention appearing to run forever and never completing again.
func (w *Worker) runPass(ctx context.Context) {
	passCtx, cancel := context.WithTimeout(ctx, w.deps.PassTimeout)
	defer cancel()
	w.RunOnce(passCtx)
}

// RunOnce applies the currently configured policy to every store once.
//
// A class that fails is logged and the pass continues: the classes are
// independent, and a corrupt archive directory is no reason to let the event
// store grow for another day.
func (w *Worker) RunOnce(ctx context.Context) {
	settings, err := w.load(ctx)
	if err != nil {
		// Pruning on a policy that could not be read would apply defaults to a
		// deployment that had configured something else, and deletion is not
		// an operation to guess at.
		slog.Error("retention skipped: settings unreadable", "error", err)
		return
	}

	policy := Resolve(settings)
	w.pushToWorkers(policy)

	w.expireQuarantine(ctx)

	for _, job := range w.jobs(policy) {
		if ctx.Err() != nil {
			// Cut short, so this is not a completed pass. Recording it as one
			// would hide a shutdown loop or a wedged store behind a metric
			// that says retention ran.
			return
		}
		w.run(ctx, job)
	}

	w.lastRun.Store(w.now().UnixNano())
}

// maxQuarantineExpiriesPerPass bounds one sweep. A quarantine nobody reviews
// grows without limit, and a pass that tried to expire all of it at once would
// hold a long write while the rest of retention waited behind it.
const maxQuarantineExpiriesPerPass = 500

// expireQuarantine moves held messages past their own deadline to EXPIRED.
//
// A failure is logged and the pass continues. Expiry is housekeeping; letting
// it stop the prunes that reclaim actual disk would be the wrong thing to
// protect.
func (w *Worker) expireQuarantine(ctx context.Context) {
	if w.deps.Quarantine == nil {
		return
	}
	expired, err := w.deps.Quarantine.Expire(ctx, w.now(), maxQuarantineExpiriesPerPass)
	if err != nil {
		slog.Error("could not expire held messages", "error", err)
		return
	}
	if expired > 0 {
		// Worth a line at info: a queue expiring in bulk means nobody is
		// working it, which is a process problem rather than a mail one.
		slog.Info("held messages expired unreviewed", "count", expired)
	}
}

// pushToWorkers hands the outbox and webhook queues their own retention. They
// prune themselves, ordered against their own live work; this is the only
// place that tells them what to keep.
func (w *Worker) pushToWorkers(p Policy) {
	if w.deps.Outbox != nil {
		w.deps.Outbox.SetRetention(Duration(p.OutboxDays))
	}
	if w.deps.Webhooks != nil {
		w.deps.Webhooks.SetRetention(Duration(p.WebhookDays))
	}
	if w.deps.Screener != nil {
		w.deps.Screener.SetRetention(Duration(p.QuarantineDays))
	}
}

func (w *Worker) jobs(p Policy) []pruneJob {
	var jobs []pruneJob

	if events := w.deps.Events; events != nil {
		jobs = append(jobs,
			pruneJob{class: classEvents, days: p.EventDays, run: events.TruncateBefore},
			pruneJob{class: classMessages, days: p.MessageDays, run: events.TruncateMessagesBefore},
			// After the event pass, so archives it has just written are
			// considered by the same cutoff that governs the rest.
			pruneJob{class: classArchives, days: p.ArchiveDays, run: events.PruneArchivesBefore},
		)
	}
	if logs := w.deps.Logs; logs != nil {
		jobs = append(jobs, pruneJob{class: classAppLogs, days: p.AppLogDays, run: logs.TruncateBefore})
	}
	if inbound := w.deps.Inbound; inbound != nil {
		jobs = append(jobs, pruneJob{class: classInbound, days: p.InboundDays, run: inbound.TruncateBefore})
	}

	return jobs
}

func (w *Worker) run(ctx context.Context, job pruneJob) {
	before, ok := Cutoff(job.days, w.now())
	if !ok {
		return
	}

	removed, err := job.run(ctx, before)
	// Counted before the error is checked: a pass that failed halfway still
	// deleted what it reports, and losing that number would make the metric
	// disagree with the store.
	w.removed.Add(removed)
	if err != nil {
		w.failures.Add(1)
		slog.Error("retention prune failed",
			"class", job.class, "before", before.Format(time.RFC3339),
			"removed_before_failure", removed, "error", err)
		return
	}
	if removed > 0 {
		slog.Info("retention pruned",
			"class", job.class, "removed", removed, "before", before.Format(time.RFC3339))
	}
}

// pruneJob is one class of data and the call that expires it. Keeping the
// class name beside the call is what lets every failure name the data it was
// working on.
type pruneJob struct {
	class string
	days  int
	run   func(context.Context, time.Time) (int64, error)
}
