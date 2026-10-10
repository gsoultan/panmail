package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	// shutdownBudget is the whole of a shutdown, from the signal to the stores
	// closing, every phase included.
	//
	// The deployment runs with terminationGracePeriodSeconds: 30, and SIGKILL
	// lands when it expires. The phases used to run one after another and add
	// up to more than that: up to 15s waiting for HTTP to drain, then up to
	// 30s for the workers, whose webhook delivery alone may keep a POST going
	// for its 20s drain grace after being told to stop. A kill partway through
	// is the failure the drain graces exist to prevent: a send or a webhook
	// cut after the other side accepted it, retried, and delivered twice.
	//
	// 25s leaves five for the stores to close and for the kubelet's own
	// clock to differ from ours.
	shutdownBudget = 25 * time.Second

	// defaultPreStopDelay is how long readiness reports not-ready before the
	// listener closes. A load balancer learns that an instance is leaving
	// from its readiness probe or from the endpoint update, and both take a
	// few seconds to arrive; a listener that closes first refuses whatever was
	// routed to it in the meantime.
	//
	// The workers carry on meanwhile, so this costs no delivery time — but it
	// does come out of the budget, and with the webhook worker's 20s drain
	// grace after it, 5s is the most it can be at 25s.
	defaultPreStopDelay = 5 * time.Second

	// requestDrainGrace is how long requests in flight get once the listener
	// has closed before their contexts are cancelled. A unary call finishes
	// in far less. A server stream — StreamLogs, which a dashboard holds open
	// for as long as it is on screen — never finishes on its own, and used to
	// hold the HTTP shutdown for its full timeout.
	requestDrainGrace = 5 * time.Second
)

// shutdownPlan is the order a gateway stops in, and what each step is bounded
// by. Every field is a function so a test can stand in for the real server.
type shutdownPlan struct {
	budget       time.Duration
	preStopDelay time.Duration
	requestGrace time.Duration

	// withdraw makes readiness report not-ready.
	withdraw func()
	// listeners stop accepting and drain what they are serving; each returns
	// when drained or when ctx expires.
	listeners []func(ctx context.Context) error
	// cancelRequests cancels the context every request was served under,
	// which is what ends a stream.
	cancelRequests func()
	stopWorkers    func()
	waitWorkers    func(timeout time.Duration) bool
	closeProviders func() error
}

// run shuts down: withdraw readiness and wait for that to be noticed, stop the
// listeners, cancel the workers, wait for both, then close the provider pool.
// The Pebble stores close after it returns, through main's defers.
//
// The listeners and the workers drain at the same time, not one after the
// other. The order that matters is kept: no new request is accepted once the
// workers have been told to stop, and nothing they use is closed until both
// have finished or the budget has run out.
func (p shutdownPlan) run() {
	deadline := time.Now().Add(p.budget)

	p.withdraw()
	if p.preStopDelay > 0 {
		slog.Info("readiness withdrawn; waiting before closing the listener", "delay", p.preStopDelay)
		time.Sleep(min(p.preStopDelay, time.Until(deadline)))
	}

	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	var drained sync.WaitGroup
	for _, stop := range p.listeners {
		drained.Go(func() {
			if err := stop(ctx); err != nil {
				slog.Warn("listener did not drain cleanly", "error", err)
			}
		})
	}
	// Streams end here, and unary calls still in flight after the grace
	// have run long enough to be cut.
	grace := time.AfterFunc(p.requestGrace, p.cancelRequests)
	defer grace.Stop()

	slog.Info("stopping background workers")
	p.stopWorkers()
	stopped := p.waitWorkers(time.Until(deadline))
	drained.Wait()
	p.cancelRequests()

	if stopped {
		slog.Info("background workers stopped")
	} else {
		slog.Warn("background workers did not stop within the shutdown budget", "budget", p.budget)
	}

	if err := p.closeProviders(); err != nil {
		slog.Error("failed to close provider connections", "error", err)
	}
}
