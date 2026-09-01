package retention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/config"
)

type call struct {
	before time.Time
	ran    bool
}

type fakeEvents struct {
	events   call
	messages call
	archives call
	err      error
}

func (f *fakeEvents) TruncateBefore(_ context.Context, before time.Time) (int64, error) {
	f.events = call{before: before, ran: true}
	return 1, f.err
}

func (f *fakeEvents) TruncateMessagesBefore(_ context.Context, before time.Time) (int64, error) {
	f.messages = call{before: before, ran: true}
	return 1, nil
}

func (f *fakeEvents) PruneArchivesBefore(_ context.Context, before time.Time) (int64, error) {
	f.archives = call{before: before, ran: true}
	return 1, nil
}

type fakePruner struct{ last call }

func (f *fakePruner) TruncateBefore(_ context.Context, before time.Time) (int64, error) {
	f.last = call{before: before, ran: true}
	return 1, nil
}

type fakeSetter struct {
	got   time.Duration
	calls int
}

func (f *fakeSetter) SetRetention(d time.Duration) {
	f.got = d
	f.calls++
}

// workerFor builds a worker over fakes with a fixed clock and a settings
// loader that does not touch the real config file.
func workerFor(cfg *config.Config, deps Deps) (*Worker, time.Time) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	w := NewWorker(deps)
	w.now = func() time.Time { return now }
	w.load = func() (*config.Config, error) { return cfg, nil }
	return w, now
}

func TestRunOncePrunesOnlyConfiguredClasses(t *testing.T) {
	tests := []struct {
		name         string
		cfg          config.AppConfig
		wantEvents   bool
		wantMessages bool
		wantArchives bool
		wantLogs     bool
		wantInbound  bool
	}{
		{
			// The upgrade case: nothing configured, so nothing new starts
			// deleting. Events still expire because that is what panmail
			// already did before any of this was settable.
			name:       "an unconfigured deployment only expires events",
			cfg:        config.AppConfig{},
			wantEvents: true,
		},
		{
			name: "each class runs when it is set",
			cfg: config.AppConfig{
				MessageRetentionDays: 30,
				ArchiveRetentionDays: 365,
				AppLogRetentionDays:  7,
				InboundRetentionDays: 90,
			},
			wantEvents:   true,
			wantMessages: true,
			wantArchives: true,
			wantLogs:     true,
			wantInbound:  true,
		},
		{
			name:       "zero means forever, so the pass does not touch it",
			cfg:        config.AppConfig{LogRetentionDays: days(0)},
			wantEvents: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events := &fakeEvents{}
			logs := &fakePruner{}
			inbound := &fakePruner{}

			w, _ := workerFor(&config.Config{App: tc.cfg}, Deps{
				Events:  events,
				Logs:    logs,
				Inbound: inbound,
			})
			w.RunOnce(t.Context())

			for _, got := range []struct {
				class string
				ran   bool
				want  bool
			}{
				{"events", events.events.ran, tc.wantEvents},
				{"messages", events.messages.ran, tc.wantMessages},
				{"archives", events.archives.ran, tc.wantArchives},
				{"app logs", logs.last.ran, tc.wantLogs},
				{"inbound", inbound.last.ran, tc.wantInbound},
			} {
				if got.ran != got.want {
					t.Errorf("%s pruned = %v; want %v", got.class, got.ran, got.want)
				}
			}
		})
	}
}

func TestRunOncePassesTheRightCutoff(t *testing.T) {
	events := &fakeEvents{}
	w, now := workerFor(
		&config.Config{App: config.AppConfig{LogRetentionDays: days(14), MessageRetentionDays: 2}},
		Deps{Events: events},
	)

	w.RunOnce(t.Context())

	if want := now.AddDate(0, 0, -14); !events.events.before.Equal(want) {
		t.Errorf("events cutoff = %v; want %v", events.events.before, want)
	}
	// Each class gets its own cutoff. Sharing one is how a two-day body
	// retention would quietly keep bodies for a fortnight.
	if want := now.AddDate(0, 0, -2); !events.messages.before.Equal(want) {
		t.Errorf("messages cutoff = %v; want %v", events.messages.before, want)
	}
}

func TestRunOncePushesRetentionToWorkers(t *testing.T) {
	outbox := &fakeSetter{}
	webhooks := &fakeSetter{}

	w, _ := workerFor(
		&config.Config{App: config.AppConfig{
			OutboxRetentionDays:  21,
			WebhookRetentionDays: days(0),
		}},
		Deps{Outbox: outbox, Webhooks: webhooks},
	)
	w.RunOnce(t.Context())

	if want := 21 * 24 * time.Hour; outbox.got != want {
		t.Errorf("outbox retention = %v; want %v", outbox.got, want)
	}
	// Zero has to be pushed too. Skipping it would leave the webhook worker on
	// its seven-day default after an administrator turned the policy off.
	if webhooks.calls != 1 || webhooks.got != 0 {
		t.Errorf("webhook retention = %v after %d calls; want 0 after 1", webhooks.got, webhooks.calls)
	}
}

func TestRunOnceSkipsEverythingWhenSettingsCannotBeRead(t *testing.T) {
	events := &fakeEvents{}
	outbox := &fakeSetter{}

	w := NewWorker(Deps{Events: events, Outbox: outbox})
	w.load = func() (*config.Config, error) { return nil, errors.New("permission denied") }

	w.RunOnce(t.Context())

	// Falling back to defaults here would apply a fortnight to a deployment
	// that had configured something else, and deletion is not an operation to
	// guess at.
	if events.events.ran {
		t.Error("pruned events on a policy that could not be read")
	}
	if outbox.calls != 0 {
		t.Error("reconfigured the outbox worker on a policy that could not be read")
	}
}

func TestRunOnceContinuesAfterAFailedClass(t *testing.T) {
	events := &fakeEvents{err: errors.New("store is busy")}
	logs := &fakePruner{}

	w, _ := workerFor(
		&config.Config{App: config.AppConfig{MessageRetentionDays: 5, AppLogRetentionDays: 5}},
		Deps{Events: events, Logs: logs},
	)
	w.RunOnce(t.Context())

	if !events.messages.ran {
		t.Error("stopped at the failed class instead of continuing")
	}
	if !logs.last.ran {
		t.Error("a failing event store stopped the app log pass")
	}
}

func TestRunOnceStopsOnCancellation(t *testing.T) {
	events := &fakeEvents{}
	logs := &fakePruner{}

	w, _ := workerFor(
		&config.Config{App: config.AppConfig{MessageRetentionDays: 5, AppLogRetentionDays: 5}},
		Deps{Events: events, Logs: logs},
	)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w.RunOnce(ctx)

	if events.events.ran || logs.last.ran {
		t.Error("started a full-store scan under a cancelled context")
	}
}

func TestStatsRecordWhatThePassDid(t *testing.T) {
	events := &fakeEvents{}
	w, now := workerFor(
		&config.Config{App: config.AppConfig{MessageRetentionDays: 5}},
		Deps{Events: events},
	)

	w.RunOnce(t.Context())

	stats := w.Stats()
	// Events and messages both had a policy, and each fake removes one.
	if stats.Removed != 2 {
		t.Errorf("removed = %d; want 2", stats.Removed)
	}
	if stats.Failures != 0 {
		t.Errorf("failures = %d; want 0", stats.Failures)
	}
	if !stats.LastRun.Equal(now) {
		t.Errorf("last run = %v; want %v", stats.LastRun, now)
	}
	if got := w.SinceLastRun(); got != 0 {
		t.Errorf("since last run = %v; want 0 right after a pass", got)
	}
}

func TestSinceLastRunMeasuresFromStartBeforeTheFirstPass(t *testing.T) {
	w := NewWorker(Deps{})
	w.startedAt = time.Date(2026, 8, 17, 6, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) }

	// Zero here would read as "ran just now" and silence the one alert that
	// catches a gateway where retention never ran at all.
	if got, want := w.SinceLastRun(), 6*time.Hour; got != want {
		t.Errorf("since last run = %v; want %v", got, want)
	}
	if !w.Stats().LastRun.IsZero() {
		t.Error("reported a last run before any pass completed")
	}
}

func TestAFailedClassIsCountedAndDoesNotCompleteSilently(t *testing.T) {
	events := &fakeEvents{err: errors.New("store is busy")}
	w, _ := workerFor(&config.Config{}, Deps{Events: events})

	w.RunOnce(t.Context())

	stats := w.Stats()
	if stats.Failures != 1 {
		t.Errorf("failures = %d; want 1", stats.Failures)
	}
	// What it managed to delete before failing still counts, or the metric
	// disagrees with the store.
	if stats.Removed != 1 {
		t.Errorf("removed = %d; want the rows it did delete to be counted", stats.Removed)
	}
}

func TestAnIncompletePassIsNotRecordedAsARun(t *testing.T) {
	tests := []struct {
		name string
		load func() (*config.Config, error)
		ctx  func(t *testing.T) context.Context
	}{
		{
			name: "settings unreadable",
			load: func() (*config.Config, error) { return nil, errors.New("permission denied") },
			ctx:  func(t *testing.T) context.Context { return t.Context() },
		},
		{
			name: "cancelled mid-pass",
			load: func() (*config.Config, error) { return &config.Config{}, nil },
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorker(Deps{Events: &fakeEvents{}})
			w.load = tc.load

			w.RunOnce(tc.ctx(t))

			// Recording these as completed passes would hide a shutdown loop
			// or an unreadable config behind a metric saying retention ran.
			if !w.Stats().LastRun.IsZero() {
				t.Error("counted an incomplete pass as a completed run")
			}
		})
	}
}

// started runs a worker in the background and stops it when the test ends.
func started(t *testing.T, deps Deps) *Worker {
	t.Helper()

	w := NewWorker(deps)
	w.load = func() (*config.Config, error) { return &config.Config{}, nil }

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Start(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Start did not return when its context was cancelled")
		}
	})
	return w
}

// awaitPass waits for a pass to complete, reading only the atomics so the test
// does not race the worker goroutine.
func awaitPass(t *testing.T, w *Worker) {
	t.Helper()

	for range 200 {
		if !w.Stats().LastRun.IsZero() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no retention pass completed")
}

func TestStartRunsItsFirstPassAfterTheStartupDelay(t *testing.T) {
	w := started(t, Deps{
		Events:       &fakeEvents{},
		Interval:     time.Hour,
		StartupDelay: time.Millisecond,
	})

	// Held back rather than skipped: a gateway restarted daily never reaches
	// its first tick, so a worker that only pruned on the tick would never
	// prune at all.
	awaitPass(t, w)
}

func TestTriggerCutsTheWaitShort(t *testing.T) {
	w := started(t, Deps{
		Events:       &fakeEvents{},
		Interval:     time.Hour,
		StartupDelay: time.Hour,
	})

	// Nothing would otherwise happen for an hour. This is what makes a
	// retention saved in the settings page take effect on save.
	w.Trigger()
	awaitPass(t, w)
}

func TestNilStoresAreSkipped(t *testing.T) {
	w, _ := workerFor(&config.Config{App: config.AppConfig{MessageRetentionDays: 5}}, Deps{})

	// A deployment without one of these stores must not panic a background
	// worker on its first pass.
	w.RunOnce(t.Context())
}

// stubExpirer records the sweep and can fail it.
type stubExpirer struct {
	calls  int
	limit  int
	result int
	err    error
}

func (s *stubExpirer) Expire(_ context.Context, _ time.Time, limit int) (int, error) {
	s.calls++
	s.limit = limit
	return s.result, s.err
}

func TestRetentionExpiresHeldMessages(t *testing.T) {
	expirer := &stubExpirer{result: 3}
	w := NewWorker(Deps{Quarantine: expirer, StartupDelay: -1})

	w.RunOnce(context.Background())

	if expirer.calls != 1 {
		t.Fatalf("expiry ran %d times, want 1", expirer.calls)
	}
	// Bounded, so a quarantine nobody reviews cannot hold a long write while
	// the rest of retention waits behind it.
	if expirer.limit <= 0 {
		t.Errorf("limit = %d, want the sweep bounded", expirer.limit)
	}
}

// Expiry is housekeeping. Letting it stop the prunes that reclaim actual disk
// would be the wrong thing to protect.
func TestAFailedExpirySweepDoesNotStopThePass(t *testing.T) {
	expirer := &stubExpirer{err: errors.New("database unreachable")}
	events := &fakeEvents{}
	w := NewWorker(Deps{Quarantine: expirer, Events: events, StartupDelay: -1})

	w.RunOnce(context.Background())

	if !events.events.ran {
		t.Error("a failed expiry sweep stopped the rest of the pass")
	}
}
