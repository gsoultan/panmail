package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type pinger struct{ err error }

func (p pinger) PingContext(context.Context) error { return p.err }

// conn stands in for db.Connection: a handle that can change, or not exist yet.
type conn struct{ db *sql.DB }

func (c *conn) GetDB() *sql.DB { return c.db }

func get(t *testing.T, h http.HandlerFunc) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

// The failure this package exists for: the old check was a static value set at
// boot, so an instance whose database had gone reported OK and kept taking
// traffic it could not serve.
func TestAnUnreachableDatabaseMeansNotReady(t *testing.T) {
	c := New()
	c.Register("database", SQLPinger(pinger{err: errors.New("connection refused")}))

	code, body := get(t, c.ReadyHandler())

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status %d with the database down; a load balancer would keep sending requests", code)
	}
	// Naming the dependency is the difference between a page that says "look
	// at the database" and one that says "something is wrong".
	failed, _ := body["failed"].([]any)
	if len(failed) != 1 || failed[0] != "database" {
		t.Errorf("failed = %v, want [database]", failed)
	}
}

// The name, and not the error behind it.
//
// This handler is on the public listener, because a load balancer has to reach
// it, so the body reaches anyone who asks. The driver's error names the
// database's user, database, host and port — an earlier version of this test
// asserted that text was present, which is how the disclosure got written.
func TestReadinessDoesNotPublishWhatItKnows(t *testing.T) {
	c := New()
	c.Register("database", SQLPinger(pinger{err: errors.New(
		"failed to connect to `user=panmail database=panmail`: db.internal:5432 (10.0.0.7): connect: connection refused")}))

	_, body := get(t, c.ReadyHandler())
	published := strings.ToLower(rawJSON(t, body))

	for _, secret := range []string{
		"user=panmail", // the database user
		"db.internal",  // the internal hostname
		"10.0.0.7",     // and its address
		"5432",         // and its port
		"connection refused",
	} {
		if strings.Contains(published, strings.ToLower(secret)) {
			t.Errorf("readiness published %q to an unauthenticated caller: %s", secret, published)
		}
	}

	// It still has to be useful: the dependency is named.
	if !strings.Contains(published, "database") {
		t.Errorf("body %s does not name the failed dependency", published)
	}
}

func TestAReachableDatabaseMeansReady(t *testing.T) {
	c := New()
	c.Register("database", SQLPinger(pinger{}))

	if code, _ := get(t, c.ReadyHandler()); code != http.StatusOK {
		t.Errorf("status %d with a healthy database", code)
	}
}

// One broken dependency is enough to stop serving, and the healthy ones must
// not mask it.
func TestOneFailedDependencyIsEnough(t *testing.T) {
	c := New()
	c.Register("database", SQLPinger(pinger{}))
	c.Register("events", func(context.Context) error { return errors.New("pebble: closed") })

	code, body := get(t, c.ReadyHandler())
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status %d with the event store broken", code)
	}
	failed, _ := body["failed"].([]any)
	if len(failed) != 1 || failed[0] != "events" {
		t.Errorf("failed = %v, want [events] — the healthy database should not appear", failed)
	}
}

// A readiness endpoint whose latency is the sum of its dependencies' will time
// out on exactly the bad day its answer is needed.
func TestProbesRunConcurrently(t *testing.T) {
	c := New()
	const each = 150 * time.Millisecond
	for _, name := range []string{"a", "b", "c", "d"} {
		c.Register(name, func(ctx context.Context) error {
			select {
			case <-time.After(each):
			case <-ctx.Done():
			}
			return nil
		})
	}

	start := time.Now()
	c.Check(context.Background())
	elapsed := time.Since(start)

	if elapsed > 3*each {
		t.Errorf("four %v probes took %v; they are running one after another", each, elapsed)
	}
}

// A probe that hangs must not hold the endpoint open past the orchestrator's
// own deadline, or the probe is recorded as a timeout with no detail at all.
func TestAHangingProbeIsBounded(t *testing.T) {
	c := New()
	c.Register("database", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	start := time.Now()
	failures := c.Check(context.Background())
	elapsed := time.Since(start)

	if elapsed > DefaultTimeout+time.Second {
		t.Errorf("a hanging probe held readiness for %v", elapsed)
	}
	if _, ok := failures["database"]; !ok {
		t.Error("a probe that never answered was treated as healthy")
	}
}

// Readiness is a point-in-time answer. A cached one keeps traffic arriving
// after the instance has stopped being able to serve it.
func TestReadinessIsNotCacheable(t *testing.T) {
	c := New()
	c.Register("database", SQLPinger(pinger{}))

	rec := httptest.NewRecorder()
	c.ReadyHandler()(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// The answer is held for a bounded window and then probed afresh. A result
// that never expired would be the static checker this package replaced; one
// that never lasted at all let any unauthenticated caller ping the database
// as often as it liked.
func TestTheAnswerIsReprobedOnceItsWindowPasses(t *testing.T) {
	var calls atomic.Int32
	c := New()
	c.cacheFor = 50 * time.Millisecond
	c.Register("database", func(context.Context) error {
		calls.Add(1)
		return nil
	})

	for range 3 {
		get(t, c.ReadyHandler())
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("probe ran %d times over 3 requests inside one window, want 1", got)
	}

	time.Sleep(2 * c.cacheFor)
	get(t, c.ReadyHandler())
	if got := calls.Load(); got != 2 {
		t.Errorf("probe ran %d times after the window passed, want 2; the answer is going stale", got)
	}
}

// /readyz is public and unauthenticated. Every request used to ping the
// database, so a flood of them was a flood of pool checkouts against the
// database the mail queue depends on.
func TestABurstOfRequestsProbesOnce(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c := New()
	c.Register("database", func(context.Context) error {
		calls.Add(1)
		<-release // hold the probe so the whole burst arrives while it runs
		return nil
	})

	const burst = 50
	var wg sync.WaitGroup
	codes := make(chan int, burst)
	for range burst {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			c.ReadyHandler()(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			codes <- rec.Code
		})
	}
	// Let the burst queue up behind the first probe, then let it answer.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(codes)

	if got := calls.Load(); got != 1 {
		t.Errorf("%d concurrent requests ran the probe %d times, want 1", burst, got)
	}
	for code := range codes {
		if code != http.StatusOK {
			t.Errorf("a request sharing the evaluation got %d", code)
		}
	}
}

// A database down for an hour used to log a warning per probe per instance
// for the hour. What an operator needs is when it changed.
func TestOnlyChangesAreLogged(t *testing.T) {
	logs := captureLogs(t)

	var down atomic.Bool
	down.Store(true)
	c := New()
	c.cacheFor = 0
	c.Register("database", func(context.Context) error {
		if down.Load() {
			return errors.New("connection refused")
		}
		return nil
	})

	for range 10 {
		get(t, c.ReadyHandler())
	}
	if got := logs.count(slog.LevelWarn); got != 1 {
		t.Errorf("ten failing probes logged %d warnings, want 1 for the change", got)
	}

	down.Store(false)
	for range 10 {
		get(t, c.ReadyHandler())
	}
	if got := logs.count(slog.LevelInfo); got != 1 {
		t.Errorf("recovery logged %d times, want once", got)
	}
	if got := logs.count(slog.LevelWarn); got != 1 {
		t.Errorf("healthy probes logged warnings: %d in total", got)
	}
}

type logCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (l *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (l *logCapture) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r)
	return nil
}
func (l *logCapture) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logCapture) WithGroup(string) slog.Handler      { return l }

func (l *logCapture) count(level slog.Level) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.records {
		if r.Level == level {
			n++
		}
	}
	return n
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	capture := &logCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return capture
}

func TestNoDependenciesIsReady(t *testing.T) {
	if code, _ := get(t, New().ReadyHandler()); code != http.StatusOK {
		t.Errorf("status %d with nothing registered", code)
	}
}

func rawJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A gateway starts before it necessarily has a database — that is how the
// first-run setup screen is served — so readiness runs against a nil handle on
// every fresh install. Pinging a nil *sql.DB dereferences it and kills the
// handler mid-response, so the caller sees a reset connection rather than a
// 503, exactly when "there is no database" is the thing being reported.
//
// Found by running the container against a database that was not there.
func TestNoDatabaseYetIsNotReadyRatherThanACrash(t *testing.T) {
	c := New()
	c.Register("database", SQL(&conn{db: nil}))

	code, body := get(t, c.ReadyHandler())
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status %d with no database configured", code)
	}
	failed, _ := body["failed"].([]any)
	if len(failed) != 1 || failed[0] != "database" {
		t.Errorf("failed = %v, want [database]", failed)
	}
}

// The handle is not fixed for the life of the process: setup installs a
// database after startup. A probe that captured the handle it was given would
// report the old one forever, so a freshly configured gateway would never
// become ready until someone restarted it.
func TestTheProbeFollowsTheConnectionRatherThanCapturingIt(t *testing.T) {
	live, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer live.Close()

	c := New()
	// What is under test is which handle the probe reads, not how long an
	// answer is held, so every request evaluates afresh.
	c.cacheFor = 0
	shared := &conn{} // nothing configured yet, as at first boot
	c.Register("database", SQL(shared))

	if code, _ := get(t, c.ReadyHandler()); code != http.StatusServiceUnavailable {
		t.Fatalf("status %d before a database was configured", code)
	}

	// What setup does.
	shared.db = live

	if code, _ := get(t, c.ReadyHandler()); code != http.StatusOK {
		t.Error("still not ready after a database was configured; the probe is " +
			"holding the handle it was built with")
	}
}
