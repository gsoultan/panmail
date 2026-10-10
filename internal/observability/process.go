package observability

import (
	"context"
	"database/sql"
	"errors"
	"runtime"

	"go.opentelemetry.io/otel/metric"
)

// ObserveProcess publishes what the gateway is doing to itself.
//
// Everything else here measures work — how many messages are waiting, how old
// the oldest is. None of it answers the question that decides whether a process
// survives a month: is it growing. A goroutine leak, a connection leak and a
// heap that never comes down all look identical from the outside — the queues
// drain, the endpoints answer, and then one morning the container is OOMKilled
// and restarted with no record of why.
//
// The pool numbers earn their place separately. A gateway sized to consume its
// database's entire connection budget delivered duplicate mail (see pkg/db),
// and the only symptom until the duplicates arrived was writes failing with
// "sorry, too many clients already". in_use pinned at the maximum, with
// wait_count climbing, says that immediately and says it before anything is
// delivered twice.
//
// pool yields whichever database the gateway is using when scraped; nil
// publishes no pool numbers at all.
func (m *Metrics) ObserveProcess(pool func() *sql.DB) error {
	var errs []error

	// Goroutines: the clearest leak signal there is. A number that climbs and
	// never falls is a worker being started and never finishing — the IMAP
	// supervisor rebuilding sessions, or a send that never returns.
	errs = append(errs, m.ObserveGauge("goroutines", "Goroutines currently running",
		func() int64 { return int64(runtime.NumGoroutine()) }))

	// Heap in use rather than the process's whole footprint: Go returns memory
	// to the OS lazily, so RSS lags and reads as a leak when there is none.
	errs = append(errs, m.ObserveGauge("heap_in_use_bytes", "Heap memory in use",
		func() int64 {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			return int64(stats.HeapInuse)
		}))

	if pool != nil {
		errs = append(errs, m.observePool(pool))
	}

	return errors.Join(errs...)
}

// observePool publishes the connection pool's numbers, reading the pool when
// scraped rather than when registered.
//
// It used to be given the handle at registration, and the gateway registers at
// boot, which on a fresh install is before setup has installed a database.
// The handle was nil, so these gauges were never registered and did not appear
// until someone restarted the process. A scrape with no database yet publishes
// none of them, rather than zeros that would read as an idle, healthy pool.
func (m *Metrics) observePool(pool func() *sql.DB) error {
	inUse, err1 := m.Meter.Int64ObservableGauge("panmail_db_connections_in_use",
		metric.WithDescription("Database connections currently checked out"))
	open, err2 := m.Meter.Int64ObservableGauge("panmail_db_connections_open",
		metric.WithDescription("Database connections open, in use or idle"))
	// Non-zero and rising means the pool bound is now the limit, not the
	// database. That is the moment to raise database.max_open_conns — or, if
	// the server cannot take more, to add an instance.
	waits, err3 := m.Meter.Int64ObservableGauge("panmail_db_connections_wait_total",
		metric.WithDescription("Requests that had to wait for a connection"))
	// The count alone does not say whether waiting costs anything. A send
	// holds no connection while it is talking to SMTP and takes one only to
	// record the result, so brief contention among two hundred concurrent
	// sends is normal and harmless — the queue still drains. This is the
	// number that separates that from a pool that is actually throttling
	// delivery: total time waited, which grows in step with the count when it
	// is fine and far faster than it when it is not.
	waited, err4 := m.Meter.Int64ObservableGauge("panmail_db_connections_wait_seconds_total",
		metric.WithDescription("Total time spent waiting for a connection"))
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return err
	}

	_, err := m.Meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			db := pool()
			if db == nil {
				return nil
			}
			stats := db.Stats()
			o.ObserveInt64(inUse, int64(stats.InUse))
			o.ObserveInt64(open, int64(stats.OpenConnections))
			o.ObserveInt64(waits, stats.WaitCount)
			o.ObserveInt64(waited, int64(stats.WaitDuration.Seconds()))
			return nil
		},
		inUse, open, waits, waited,
	)
	return err
}
