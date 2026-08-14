package usecases

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Every deploy is a SIGTERM arriving while the queue is busy.
//
// Cancelling the worker's context used to cancel every send with it, mid-SMTP.
// Measured against a real gateway: one signal abandoned 188 in-flight messages
// and deferred 184 more. Nothing was lost — the lease brings a claimed row
// back — but a send cancelled after DATA was accepted and before the reply
// arrived has already been delivered, and panmail cannot know that, so it goes
// out again. Every deploy was a chance to send the same email twice.
//
// These test the context plumbing that decides it, which is the part that was
// wrong. Whether a batch drains end to end is checked against a real gateway,
// not here.

// batchContext reproduces what processPending builds: a context shutdown does
// not reach, with a bounded grace once shutdown starts.
func batchContext(ctx context.Context, grace time.Duration) (context.Context, func()) {
	batchCtx, cancelBatch := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, func() {
		time.AfterFunc(grace, cancelBatch)
	})
	return batchCtx, func() { stop(); cancelBatch() }
}

func TestAnInFlightSendSurvivesTheShutdownSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	batchCtx, done := batchContext(ctx, time.Minute)
	defer done()

	// The signal arrives.
	cancel()

	// The send in progress must not see it. This is the whole fix: before it,
	// batchCtx was ctx, and this assertion failed the instant cancel() ran.
	select {
	case <-batchCtx.Done():
		t.Fatal("shutdown cancelled a send that was already in progress; " +
			"a message accepted by the provider but cut before the reply is sent again")
	case <-time.After(50 * time.Millisecond):
	}
}

// The grace has to end, or a hung provider holds the process open past
// whatever the orchestrator is willing to wait and the drain becomes a SIGKILL.
func TestTheDrainIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	const grace = 150 * time.Millisecond
	batchCtx, done := batchContext(ctx, grace)
	defer done()

	cancel()

	select {
	case <-batchCtx.Done():
	case <-time.After(grace + 2*time.Second):
		t.Fatal("the drain never ended; shutdown would wait on a hung send indefinitely")
	}
}

// Nothing about this may change normal running. A batch that is not shutting
// down must not acquire a deadline it did not have.
func TestARunningBatchIsNotOnAClock(t *testing.T) {
	ctx := context.Background()

	batchCtx, done := batchContext(ctx, 10*time.Millisecond)
	defer done()

	if _, ok := batchCtx.Deadline(); ok {
		t.Error("a batch running normally was given a deadline")
	}
	select {
	case <-batchCtx.Done():
		t.Fatal("a batch was cancelled while nothing was shutting down")
	case <-time.After(100 * time.Millisecond):
	}
}

// A message that has not started has not been attempted, so leaving it costs a
// lease period and risks nothing. Waiting for all 500 of a claimed batch would
// turn every shutdown into a full drain.
func TestAMessageThatNeverStartedIsLeftForTheLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	batchCtx, done := batchContext(ctx, time.Minute)
	defer done()

	var started atomic.Int64
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // saturated: the next message cannot start

	cancel()

	// This is the select in processPending. The waiting message gives up on
	// the parent context, deliberately, rather than the batch one.
	select {
	case sem <- struct{}{}:
		started.Add(1)
	case <-ctx.Done():
	}

	if started.Load() != 0 {
		t.Error("a message started after the shutdown signal")
	}
	// And the batch context is still live for the one that did start.
	if batchCtx.Err() != nil {
		t.Error("the in-flight send was cancelled along with the queued one")
	}
}
