package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/health"
)

// The deployment gives a pod 30 seconds between SIGTERM and SIGKILL. The
// phases used to run in sequence and add up to more: 15s for HTTP, then 30s
// for the workers, of which the webhook worker alone may spend 20s finishing
// a POST it had started.
func TestTheShutdownBudgetFitsTheGracePeriod(t *testing.T) {
	const (
		terminationGracePeriod = 30 * time.Second
		webhookDrainGrace      = 20 * time.Second // internal/webhook/worker
	)
	if shutdownBudget > terminationGracePeriod-5*time.Second {
		t.Errorf("a %v budget leaves no room inside a %v grace period for the stores to close",
			shutdownBudget, terminationGracePeriod)
	}
	if defaultPreStopDelay+webhookDrainGrace > shutdownBudget {
		t.Errorf("pre-stop %v plus the webhook drain %v exceeds the %v budget",
			defaultPreStopDelay, webhookDrainGrace, shutdownBudget)
	}
}

// Served the way main serves: every request under a context that shutdown
// cancels.
func serveForShutdown(t *testing.T, h http.Handler) (srv *http.Server, addr string, cancelRequests func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv = &http.Server{
		Handler:     h,
		BaseContext: func(net.Listener) context.Context { return requestCtx },
	}
	go func() { _ = srv.Serve(ln) }()
	return srv, "http://" + ln.Addr().String(), cancel
}

// Before: SIGTERM flipped liveness and left /readyz answering 200, then closed
// the listener at once, so the load balancer went on routing to a port that
// refused; and an open log stream held the HTTP drain for its full 15s.
func TestShutdownWithdrawsReadinessFirstAndEndsStreams(t *testing.T) {
	readiness := health.New()
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", readiness.ReadyHandler())

	streaming := make(chan struct{})
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(streaming)
		<-r.Context().Done() // a server stream: ends only when told to
	})

	srv, base, cancelRequests := serveForShutdown(t, mux)

	if resp, err := http.Get(base + "/readyz"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("not ready before shutdown: %v %v", resp, err)
	}

	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		resp, err := http.Get(base + "/stream")
		if err == nil {
			_, _ = resp.Body.Read(make([]byte, 1))
			resp.Body.Close()
		}
	}()
	<-streaming

	const (
		preStop = 300 * time.Millisecond
		grace   = 200 * time.Millisecond
		budget  = 5 * time.Second
	)
	var workersStopped atomic.Bool
	plan := shutdownPlan{
		budget:         budget,
		preStopDelay:   preStop,
		requestGrace:   grace,
		withdraw:       readiness.Drain,
		listeners:      []func(context.Context) error{srv.Shutdown},
		cancelRequests: cancelRequests,
		stopWorkers:    func() { workersStopped.Store(true) },
		waitWorkers:    func(time.Duration) bool { return true },
		closeProviders: func() error { return nil },
	}

	start := time.Now()
	done := make(chan struct{})
	go func() {
		plan.run()
		close(done)
	}()

	// During the pre-stop delay the listener is still open and says not ready.
	time.Sleep(preStop / 3)
	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("the listener closed before readiness had time to be noticed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/readyz answered %d after shutdown began; the load balancer keeps routing here", resp.StatusCode)
	}

	select {
	case <-done:
	case <-time.After(budget):
		t.Fatal("shutdown did not finish within its budget")
	}
	elapsed := time.Since(start)
	<-streamDone

	if elapsed < preStop {
		t.Errorf("shutdown took %v, shorter than the pre-stop delay %v", elapsed, preStop)
	}
	// An open stream must not hold the drain for the budget: it ends once the
	// grace for requests in flight has passed.
	if elapsed > preStop+grace+time.Second {
		t.Errorf("shutdown took %v with a stream open; the stream held the drain", elapsed)
	}
	if !workersStopped.Load() {
		t.Error("the workers were never stopped")
	}
}

// HTTP and the workers drain at the same time and the whole thing ends inside
// the budget, however long either would take on its own. The provider pool
// closes only after the workers have been waited for.
func TestShutdownStaysInsideItsBudget(t *testing.T) {
	const budget = 600 * time.Millisecond

	var (
		mu     sync.Mutex
		events []string
	)
	record := func(e string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}

	plan := shutdownPlan{
		budget:       budget,
		preStopDelay: 50 * time.Millisecond,
		requestGrace: 50 * time.Millisecond,
		withdraw:     func() { record("withdraw") },
		listeners: []func(context.Context) error{func(ctx context.Context) error {
			record("listener closing")
			<-ctx.Done() // a connection that never goes idle
			record("listener gave up")
			return ctx.Err()
		}},
		cancelRequests: func() {},
		stopWorkers:    func() { record("workers cancelled") },
		waitWorkers: func(timeout time.Duration) bool {
			time.Sleep(timeout) // a worker that never finishes
			record("workers waited")
			return false
		},
		closeProviders: func() error { record("providers closed"); return errors.New("ignored") },
	}

	start := time.Now()
	plan.run()
	elapsed := time.Since(start)

	if elapsed > budget+200*time.Millisecond {
		t.Errorf("shutdown took %v against a %v budget; SIGKILL would land partway", elapsed, budget)
	}

	mu.Lock()
	defer mu.Unlock()
	index := func(e string) int {
		for i, got := range events {
			if got == e {
				return i
			}
		}
		t.Fatalf("%q never happened: %v", e, events)
		return -1
	}
	if index("withdraw") != 0 {
		t.Errorf("readiness was not withdrawn first: %v", events)
	}
	if index("workers cancelled") > index("listener gave up") {
		t.Errorf("the workers waited for HTTP to finish draining before being told to stop: %v", events)
	}
	if index("providers closed") < index("workers waited") {
		t.Errorf("the provider pool closed while workers could still be using it: %v", events)
	}
}
