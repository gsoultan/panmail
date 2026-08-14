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
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"
)

// DefaultTimeout bounds a probe. Long enough to ride out a slow query, short
// enough that the orchestrator's own probe deadline is not what expires.
const DefaultTimeout = 2 * time.Second

// Probe reports whether one dependency is usable. A nil error means it is.
type Probe func(context.Context) error

// Checker runs the probes that decide whether this instance can serve.
type Checker struct {
	timeout time.Duration

	mu     sync.RWMutex
	probes map[string]Probe
}

func New() *Checker {
	return &Checker{timeout: DefaultTimeout, probes: map[string]Probe{}}
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
// The body is the point. A bare 503 at three in the morning says only that
// something is wrong; {"database":"connection refused"} says where to look.
func (c *Checker) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		failures := c.Check(r.Context())

		w.Header().Set("Content-Type", "application/json")
		// Readiness is a point-in-time answer, and a cached one is worse than
		// none: it keeps traffic arriving after the instance has stopped being
		// able to serve it.
		w.Header().Set("Cache-Control", "no-store")

		if len(failures) == 0 {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready"})
			return
		}

		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "not ready",
			"failed":  slices.Sorted(maps.Keys(failures)),
			"details": failures,
		})
	}
}

// Pinger is the part of *sql.DB readiness needs.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// SQL probes the database.
//
// PingContext takes a connection from the pool, so this also reports the case
// where the server is fine and this instance cannot reach it anyway because its
// own pool is exhausted. That is the right answer: an instance that cannot get
// a connection within the timeout cannot serve a request either.
func SQL(db Pinger) Probe {
	return func(ctx context.Context) error { return db.PingContext(ctx) }
}
