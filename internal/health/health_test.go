package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type pinger struct{ err error }

func (p pinger) PingContext(context.Context) error { return p.err }

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
	c.Register("database", SQL(pinger{err: errors.New("connection refused")}))

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
	c.Register("database", SQL(pinger{err: errors.New(
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
	c.Register("database", SQL(pinger{}))

	if code, _ := get(t, c.ReadyHandler()); code != http.StatusOK {
		t.Errorf("status %d with a healthy database", code)
	}
}

// One broken dependency is enough to stop serving, and the healthy ones must
// not mask it.
func TestOneFailedDependencyIsEnough(t *testing.T) {
	c := New()
	c.Register("database", SQL(pinger{}))
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
	c.Register("database", SQL(pinger{}))

	rec := httptest.NewRecorder()
	c.ReadyHandler()(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// The probe runs per request; a stale result is the bug being fixed.
func TestEveryRequestRunsTheProbe(t *testing.T) {
	var calls atomic.Int32
	c := New()
	c.Register("database", func(context.Context) error {
		calls.Add(1)
		return nil
	})

	for range 3 {
		get(t, c.ReadyHandler())
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("probe ran %d times over 3 requests; the answer is being cached", got)
	}
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
