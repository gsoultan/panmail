package usecases

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
)

const (
	// messageLease is how long a claim lasts without being renewed: from the
	// claim itself, and again from every renewal while a message is being
	// worked on.
	//
	// It used to be one fixed span from the moment the batch was claimed, and
	// that was wrong for most of the batch. 500 rows are claimed and 200 run at
	// once, so the rest wait for a slot, and the lease was never extended -- a
	// row that started after its lease had lapsed could be claimed and sent by
	// another replica at the same time as this one. A lease is now confirmed
	// and renewed when a message starts and kept alive while it runs, so this
	// only has to be long enough to ride out a renewal or two that failed.
	// Short is better beyond that: it is also how long a crashed worker's mail
	// waits before someone else picks it up.
	messageLease = 2 * time.Minute

	// leaseRenewEvery leaves room for two renewals to fail before the lease
	// lapses.
	leaseRenewEvery = messageLease / 3
)

// heldLease is a claim this worker has confirmed, kept alive for as long as
// the message is being worked on.
type heldLease struct {
	// ctx is cancelled if the claim is lost, so a send in progress stops
	// rather than racing the worker that now owns the message.
	ctx    context.Context
	cancel context.CancelFunc
	lost   atomic.Bool

	done    chan struct{}
	stopped chan struct{}
}

// holdLease confirms that this worker still owns e and starts keeping its
// lease alive, or reports that the message is not this worker's to send.
//
// Confirmed at the start rather than trusted from the claim, because a message
// may have waited for a concurrency slot long enough for its lease to lapse and
// another replica to claim it. A renewal that cannot be confirmed either way --
// the database did not answer -- is treated as not ours: leaving the message
// costs one lease period, sending it twice cannot be taken back.
func (w *queueWorker) holdLease(ctx context.Context, e *entities.OutboxEmail) (*heldLease, bool) {
	ok, err := w.renewLease(ctx, e)
	if err != nil {
		slog.Warn("could not confirm the claim on an outbox message; leaving it for the lease",
			"error", err, "id", e.ID)
		return nil, false
	}
	if !ok {
		slog.Info("outbox message was claimed elsewhere while it waited; not sending it here", "id", e.ID)
		return nil, false
	}

	l := &heldLease{done: make(chan struct{}), stopped: make(chan struct{})}
	l.ctx, l.cancel = context.WithCancel(ctx)

	go func() {
		defer close(l.stopped)
		ticker := time.NewTicker(w.leaseRenewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-l.done:
				return
			case <-ticker.C:
			}

			ok, err := w.renewLease(ctx, e)
			if err != nil {
				// One failed renewal is not a lost claim: the lease has room
				// for this, and the next tick tries again.
				slog.Warn("failed to renew the lease on an outbox message", "error", err, "id", e.ID)
				continue
			}
			if !ok {
				l.lost.Store(true)
				slog.Error("lost the claim on an outbox message while sending it; stopping",
					"id", e.ID)
				l.cancel()
				return
			}
		}
	}()
	return l, true
}

// renewLease extends e's lease by messageLease from now. Bounded like the rest
// of the bookkeeping, and not cancelled with the batch: a send draining after
// shutdown began still needs its lease.
func (w *queueWorker) renewLease(ctx context.Context, e *entities.OutboxEmail) (bool, error) {
	renewCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookkeepingTimeout)
	defer cancel()
	return w.outboxRepo.RenewClaim(renewCtx, e.ID, e.ClaimToken, time.Now().Add(w.lease))
}

// release stops renewing. The lease is left to run out rather than cleared:
// settling the row clears the claim, and a row that was not settled is exactly
// the one the lease exists to bring back.
func (l *heldLease) release() {
	close(l.done)
	<-l.stopped
	l.cancel()
}
