package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A worker that has stopped is indistinguishable from a worker with nothing to
// do. That is what makes this worth a supervisor rather than a log line: the
// process stays up, the database is fine so readiness stays green, and mail
// simply is not sent. Recovering the panic and letting the goroutine end —
// which is what this did — turns a crash into a silent outage.

// waitFor gives a condition a bounded chance to become true.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAPanickedWorkerIsBroughtBack(t *testing.T) {
	before := workerRestarts.Load()

	var runs atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	runWorker(&wg, ctx, "test-worker", func() {
		n := runs.Add(1)
		if n <= 2 {
			panic("boom")
		}
		// Third run behaves; hold until the test is done, as a real worker
		// holds until its context is cancelled.
		<-ctx.Done()
	})

	waitFor(t, "the worker to be restarted twice and then stay up", func() bool {
		return runs.Load() >= 3
	})

	// The restarts have to be countable, because nothing else distinguishes a
	// gateway that is sending mail from one that has not been for ten minutes.
	if got := workerRestarts.Load() - before; got < 2 {
		t.Errorf("recorded %d restarts, want at least 2", got)
	}

	cancel()
	wg.Wait()
}

// A worker that returns cleanly has finished on purpose — for all of these,
// because the context was cancelled. Restarting it would be a spin.
func TestAWorkerThatReturnsCleanlyIsNotRestarted(t *testing.T) {
	var runs atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	runWorker(&wg, ctx, "test-worker", func() { runs.Add(1) })

	wg.Wait()
	if got := runs.Load(); got != 1 {
		t.Errorf("ran %d times; a clean return was treated as a failure", got)
	}
}

// Shutdown must not be held up by a worker waiting out its backoff, and a
// worker panicking on every pass must not keep restarting after the context is
// done.
func TestCancellationStopsTheRestartLoop(t *testing.T) {
	var runs atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	runWorker(&wg, ctx, "always-panics", func() {
		runs.Add(1)
		panic("boom")
	})

	waitFor(t, "the first panic", func() bool { return runs.Load() >= 1 })
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(workerRestartDelay + 3*time.Second):
		t.Fatal("the supervisor did not stop when the context was cancelled")
	}

	settled := runs.Load()
	time.Sleep(2 * workerRestartDelay)
	if got := runs.Load(); got != settled {
		t.Errorf("the worker ran again after cancellation (%d -> %d)", settled, got)
	}
}

// The backoff is what keeps a permanently broken worker from becoming a busy
// loop writing stack traces.
func TestRestartsAreSpacedOut(t *testing.T) {
	var runs atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	runWorker(&wg, ctx, "always-panics", func() {
		runs.Add(1)
		panic("boom")
	})

	waitFor(t, "the first panic", func() bool { return runs.Load() >= 1 })

	// Well inside the first backoff, so an unthrottled loop would have run
	// thousands of times by now.
	time.Sleep(workerRestartDelay / 2)
	if got := runs.Load(); got > 2 {
		t.Errorf("ran %d times within half the backoff; the restarts are not spaced", got)
	}

	cancel()
	wg.Wait()
}

// runOnce reports whether the worker panicked, which is what the supervisor
// branches on. Getting this backwards would either spin on clean exits or
// abandon crashed workers — the bug being fixed.
func TestRunOnceDistinguishesAPanicFromAReturn(t *testing.T) {
	if runOnce("clean", func() {}) {
		t.Error("a clean return was reported as a panic")
	}
	if !runOnce("panicky", func() { panic("boom") }) {
		t.Error("a panic was reported as a clean return")
	}
}
