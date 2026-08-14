package observability

import (
	"database/sql"
	"errors"
	"runtime"
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
func (m *Metrics) ObserveProcess(db *sql.DB) error {
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

	if db != nil {
		errs = append(errs,
			m.ObserveGauge("db_connections_in_use", "Database connections currently checked out",
				func() int64 { return int64(db.Stats().InUse) }),
			m.ObserveGauge("db_connections_open", "Database connections open, in use or idle",
				func() int64 { return int64(db.Stats().OpenConnections) }),
			// Non-zero and rising means the pool bound is now the limit, not
			// the database. That is the moment to raise database.max_open_conns
			// — or, if the server cannot take more, to add an instance.
			m.ObserveGauge("db_connections_wait_total", "Requests that had to wait for a connection",
				func() int64 { return db.Stats().WaitCount }),
		)
	}

	return errors.Join(errs...)
}
