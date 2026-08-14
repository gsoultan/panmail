package usecases

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// What happens after a send is what decides whether a message is delivered once.
//
// The send is over by the time these writes run, so a failure here does not
// fail the message — it loses the record of it. The row keeps its old state,
// the lease expires, another worker claims it, and someone gets a second copy
// of an email they already received.
//
// This was not hypothetical. Two gateways sharing one PostgreSQL exhausted the
// server's connections, the deletes in that window failed with "sorry, too many
// clients already", and six of twenty-one thousand messages arrived twice.

// tooManyClients is the error that actually caused it.
var tooManyClients = errors.New(`server error: FATAL: sorry, too many clients already (SQLSTATE 53300)`)

func TestBookkeepingRetriesATransientFailure(t *testing.T) {
	var calls atomic.Int32

	bookkeep(context.Background(), "delete outbox email", "msg-1", func(context.Context) error {
		// Fails once, the way a connection blip does, then succeeds.
		if calls.Add(1) == 1 {
			return tooManyClients
		}
		return nil
	})

	if got := calls.Load(); got != 2 {
		t.Errorf("write attempted %d times; a single attempt leaves the row for the lease "+
			"to re-claim and the message is sent twice", got)
	}
}

func TestBookkeepingDoesNotRetryWhatSucceeded(t *testing.T) {
	var calls atomic.Int32

	bookkeep(context.Background(), "delete outbox email", "msg-1", func(context.Context) error {
		calls.Add(1)
		return nil
	})

	if got := calls.Load(); got != 1 {
		t.Errorf("wrote %d times, want 1: re-running a settled write is pointless load", got)
	}
}

// A database that is genuinely down must not hold a worker forever. The lease
// is the backstop; this only exists to ride out a blip.
func TestBookkeepingGivesUpAndLetsTheLeaseTakeOver(t *testing.T) {
	var calls atomic.Int32

	start := time.Now()
	bookkeep(context.Background(), "delete outbox email", "msg-1", func(context.Context) error {
		calls.Add(1)
		return tooManyClients
	})
	elapsed := time.Since(start)

	if got := calls.Load(); got != bookkeepingAttempts {
		t.Errorf("attempted %d times, want %d", got, bookkeepingAttempts)
	}
	// Bounded: a worker blocked here is a worker not sending mail.
	if elapsed > 3*time.Second {
		t.Errorf("gave up after %v; too long to hold a worker on a dead database", elapsed)
	}
}

// The batch context is cancelled on shutdown, and shutdown is exactly when this
// matters: the send has happened and the row still has to be settled. Writing
// through a cancelled context would fail every attempt and resend on restart.
func TestBookkeepingSurvivesACancelledBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var wrote atomic.Bool
	bookkeep(ctx, "delete outbox email", "msg-1", func(c context.Context) error {
		if c.Err() != nil {
			return c.Err()
		}
		wrote.Store(true)
		return nil
	})

	if !wrote.Load() {
		t.Error("the write was skipped because the batch was cancelled; the message was " +
			"already sent, so it will be sent again after a restart")
	}
}
