// Package observability exposes what a gateway operator needs to see.
//
// The failures this system has are quiet ones. A stalled outbox still returns
// 200 to every caller; a webhook queue that stops draining looks identical to
// one with nothing to do; an IMAP connection that hangs keeps the process
// healthy and the mailbox unread. None of them raise an error, so none of them
// are visible in logs until someone asks why mail stopped.
//
// What makes those legible is not a count but an *age*. Five hundred pending
// messages is a busy minute or a dead worker, and the number cannot tell you
// which. The oldest one being forty minutes old can only mean one thing.
package observability

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// QueueStats is what a queue can say about itself.
type QueueStats struct {
	// Pending is everything not yet in a terminal state.
	Pending int64
	// Oldest is the age of the oldest such item, zero when the queue is empty.
	// This is the signal that separates a busy queue from a stalled one.
	Oldest time.Duration
}

// QueueSource reports the state of one queue on demand.
//
// Read at scrape time rather than maintained as a counter, because a counter
// drifts: it is incremented by code that can crash between the write and the
// increment, and it starts at zero after a restart even when the queue is
// full. A COUNT with an index behind it is cheap at scrape intervals and is
// always the truth.
type QueueSource func(ctx context.Context) (QueueStats, error)

// Metrics owns the meter and the Prometheus registry behind /metrics.
type Metrics struct {
	Meter    metric.Meter
	registry *prometheus.Registry
	provider *sdkmetric.MeterProvider
}

// New builds a meter whose measurements are exposed in Prometheus format.
//
// The same meter is handed to gsmail's otelgs interceptors, which are already
// instrumented for sends and receives — without a provider configured those
// measurements were being recorded into a no-op and discarded.
func New() (*Metrics, error) {
	registry := prometheus.NewRegistry()

	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return nil, err
	}

	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))

	return &Metrics{
		Meter:    provider.Meter("panmail"),
		registry: registry,
		provider: provider,
	}, nil
}

// Handler serves the metrics in Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// SetGlobal installs this provider as the process-wide one.
//
// This is what makes gsmail's instrumentation appear. otelgs builds its
// interceptors against otel.Meter(...) — the global provider — so every send
// and receive it already measures was being recorded into a no-op and thrown
// away. One call turns that on.
//
// Deliberately separate from New rather than a side effect of construction:
// installing a global is the kind of thing that should be visible at the call
// site, not discovered later by someone wondering where the metrics come from.
func (m *Metrics) SetGlobal() {
	otel.SetMeterProvider(m.provider)
}

// Shutdown flushes and releases the provider.
func (m *Metrics) Shutdown(ctx context.Context) error {
	if m == nil || m.provider == nil {
		return nil
	}
	return m.provider.Shutdown(ctx)
}

// ObserveQueue publishes the depth and the age of a queue's oldest item.
//
// Registered as observable gauges, so the value is fetched when something
// scrapes rather than pushed on every write — a queue's depth is a fact about
// the moment you ask, not an event worth recording each time it changes.
//
// A source that errors leaves the gauge unreported for that scrape rather than
// reporting zero. Zero is indistinguishable from "empty and healthy", which is
// the one reading that must not be produced by a database that is down.
func (m *Metrics) ObserveQueue(name, description string, source QueueSource) error {
	depth, err := m.Meter.Int64ObservableGauge(
		"panmail_"+name+"_pending",
		metric.WithDescription(description+": items not yet in a terminal state"),
	)
	if err != nil {
		return err
	}

	oldest, err := m.Meter.Float64ObservableGauge(
		"panmail_"+name+"_oldest_seconds",
		metric.WithDescription(description+": age of the oldest such item, which is what separates a busy queue from a stalled one"),
	)
	if err != nil {
		return err
	}

	_, err = m.Meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			stats, err := source(ctx)
			if err != nil {
				// Reported as an error rather than as zero. The alert an
				// operator writes is "oldest > 15m"; publishing zero when the
				// database is unreachable would silence exactly the alert that
				// should be firing.
				return err
			}
			o.ObserveInt64(depth, stats.Pending)
			o.ObserveFloat64(oldest, stats.Oldest.Seconds())
			return nil
		},
		depth, oldest,
	)
	return err
}

// ObserveGauge publishes a single value read on demand, for things that are a
// count of something live rather than a queue — open IMAP sessions, say.
func (m *Metrics) ObserveGauge(name, description string, read func() int64) error {
	gauge, err := m.Meter.Int64ObservableGauge(
		"panmail_"+name,
		metric.WithDescription(description),
	)
	if err != nil {
		return err
	}

	_, err = m.Meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			o.ObserveInt64(gauge, read())
			return nil
		},
		gauge,
	)
	return err
}
