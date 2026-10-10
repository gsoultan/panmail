// Package health answers two different questions that are easy to conflate.
//
// Liveness asks whether this process is still working, and the remedy for "no"
// is to restart it. Readiness asks whether it can serve a request right now,
// and the remedy for "no" is to stop sending it traffic. Wiring a dependency
// check to liveness gets this exactly wrong: a database failover would fail the
// check on every instance at once, and an orchestrator would respond by
// restarting the entire fleet against a database that is already struggling,
// discarding every warm pool and cache on the way.
//
// So the database belongs to readiness. panmail served a single /healthz backed
// by a static value — set to serving at boot, cleared at shutdown, checking
// nothing in between — which meant an instance whose database was unreachable
// reported OK and kept taking traffic it could not serve.
package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// DefaultTimeout bounds a probe. Long enough to ride out a slow query, short
// enough that the orchestrator's own probe deadline is not what expires.
const DefaultTimeout = 2 * time.Second

// Probe reports whether one dependency is usable. A nil error means it is.
type Probe func(context.Context) error

// DefaultCacheFor is how long one evaluation answers /readyz.
//
// The endpoint is public and unauthenticated, because a load balancer has to
// reach it, and every request used to ping the database. Anyone could turn
// that into a stream of pool checkouts aimed at the same database the mail is
// waiting on. A second is far below any orchestrator's probe period, so the
// answer an orchestrator acts on is never meaningfully older than it was.
const DefaultCacheFor = time.Second

// Checker runs the probes that decide whether this instance can serve.
type Checker struct {
	timeout  time.Duration
	cacheFor time.Duration

	mu     sync.RWMutex
	probes map[string]Probe

	// Concurrent requests share one evaluation rather than each running the
	// probes, and an evaluation answers for cacheFor afterwards.
	flight   singleflight.Group
	cacheMu  sync.Mutex
	cached   map[string]string
	cachedAt time.Time

	// The failing set from the previous evaluation, for logging changes
	// rather than every probe. Guarded by cacheMu.
	lastFailed []string
	evaluated  bool

	draining atomic.Bool
}

func New() *Checker {
	return &Checker{timeout: DefaultTimeout, cacheFor: DefaultCacheFor, probes: map[string]Probe{}}
}

// Drain makes readiness answer not-ready from now on, whatever the probes say.
//
// Shutdown calls it first. A load balancer only stops routing to an instance
// once readiness says so, and until it does it keeps sending requests to a
// listener that is about to close. Saying so first, then waiting for that to
// propagate, is what lets the instance leave without anyone being refused.
func (c *Checker) Drain() {
	if c.draining.CompareAndSwap(false, true) {
		slog.Info("readiness withdrawn: shutting down")
	}
}

// Register adds a dependency to readiness. The name is what an operator reads
// in the failure body, so it should name the thing, not the check.
func (c *Checker) Register(name string, p Probe) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probes[name] = p
}

// Check runs every probe and returns the failures by name. Probes run
// concurrently: they are independent, and a readiness endpoint that takes the
// sum of its dependencies' latencies will time out on a bad day precisely when
// its answer matters.
func (c *Checker) Check(ctx context.Context) map[string]string {
	c.mu.RLock()
	probes := maps.Clone(c.probes)
	c.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var (
		mu       sync.Mutex
		failures = map[string]string{}
		wg       sync.WaitGroup
	)
	for name, probe := range probes {
		wg.Add(1)
		go func(name string, probe Probe) {
			defer wg.Done()
			if err := probe(ctx); err != nil {
				mu.Lock()
				failures[name] = err.Error()
				mu.Unlock()
			}
		}(name, probe)
	}
	wg.Wait()

	return failures
}

// ReadyHandler serves readiness: 200 when every dependency answers, 503 with
// the names of those that did not.
//
// The names, and only the names. This handler is mounted on the public
// listener — a load balancer has to reach it — so the body reaches anyone who
// asks, and the underlying errors are not fit to publish. A driver reports a
// failed connection as
//
//	failed to connect to `user=panmail database=panmail`:
//	  db.internal:5432 ... connect: connection refused
//
// which hands an unauthenticated caller the database's user, name, host and
// port. Returning that was a diagnostic written without noticing where it would
// be served from.
//
// The detail still exists, in the log, where the operator debugging at three in
// the morning is looking anyway and an internet caller is not.
func (c *Checker) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Readiness is a point-in-time answer, and one cached downstream is
		// worse than none: it keeps traffic arriving after the instance has
		// stopped being able to serve it. The second this process holds an
		// answer for is its own, bounded, and never outlives a drain.
		w.Header().Set("Cache-Control", "no-store")

		if c.draining.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "shutting down"})
			return
		}

		failures := c.evaluate(r.Context())
		if len(failures) == 0 {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready"})
			return
		}

		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "not ready",
			"failed": slices.Sorted(maps.Keys(failures)),
		})
	}
}

// evaluate answers from the last evaluation while it is fresh, and otherwise
// runs the probes once for every request that is waiting on them.
func (c *Checker) evaluate(ctx context.Context) map[string]string {
	c.cacheMu.Lock()
	if c.evaluated && time.Since(c.cachedAt) < c.cacheFor {
		failures := c.cached
		c.cacheMu.Unlock()
		return failures
	}
	c.cacheMu.Unlock()

	v, _, _ := c.flight.Do("ready", func() (any, error) {
		// Detached from the request that happened to start it: the others
		// sharing the result must not fail because that one caller hung up.
		// Check bounds the probes with its own timeout.
		failures := c.Check(context.WithoutCancel(ctx))

		c.cacheMu.Lock()
		defer c.cacheMu.Unlock()
		c.logChange(failures)
		c.cached, c.cachedAt, c.evaluated = failures, time.Now(), true
		return failures, nil
	})
	return v.(map[string]string)
}

// logChange reports a change in what is failing, rather than every failing
// probe. An orchestrator probes every few seconds, and a database that is down
// for an hour used to write a warning per probe per instance for that hour,
// which buried the one line saying when it started. Caller holds cacheMu.
func (c *Checker) logChange(failures map[string]string) {
	failed := slices.Sorted(maps.Keys(failures))
	if c.evaluated && slices.Equal(failed, c.lastFailed) {
		return
	}

	if len(failed) == 0 {
		// Nothing to say at boot, only on recovery.
		if c.evaluated {
			slog.Info("readiness restored", "previously_failed", c.lastFailed)
		}
	} else {
		for _, name := range failed {
			slog.Warn("readiness check failed", "dependency", name, "error", failures[name])
		}
	}
	c.lastFailed = failed
}

// Pinger is the part of *sql.DB readiness needs.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Handle yields whichever database the gateway is currently using.
//
// An interface rather than a *sql.DB because the handle is not fixed for the
// life of the process. A gateway starts before it necessarily has a database —
// that is how the first-run setup screen can be served at all — and setup
// installs one afterwards. A probe holding the handle it was given at startup
// would go on reporting the state of a database that has since been replaced,
// so a freshly configured instance would never become ready until someone
// restarted it.
type Handle interface {
	GetDB() *sql.DB
}

// SQL probes whatever database the gateway currently has.
//
// PingContext takes a connection from the pool, so this also reports the case
// where the server is fine and this instance cannot reach it anyway because its
// own pool is exhausted. That is the right answer: an instance that cannot get
// a connection within the timeout cannot serve a request either.
func SQL(h Handle) Probe {
	return func(ctx context.Context) error {
		db := h.GetDB()
		// Not ready, rather than a panic. Pinging a nil *sql.DB dereferences
		// it, and the handler that does so dies mid-response — so the caller
		// sees a reset connection instead of a 503, exactly when the database
		// being absent is the thing readiness exists to report.
		if db == nil {
			return errors.New("no database is configured")
		}
		return db.PingContext(ctx)
	}
}

// SQLPinger probes a database handle that will not change, for callers holding
// one directly.
func SQLPinger(db Pinger) Probe {
	return func(ctx context.Context) error {
		if db == nil {
			return errors.New("no database is configured")
		}
		return db.PingContext(ctx)
	}
}
