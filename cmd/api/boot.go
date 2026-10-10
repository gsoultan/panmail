package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gsoultan/panmail/pkg/db"
)

// How long a configured gateway keeps trying to reach its database at boot
// before giving up, and how far apart the attempts are.
//
// A gateway that could not connect at boot used to log it and carry on with no
// database at all, and never try again. /healthz answered 200, because liveness
// checks nothing external on purpose, and /readyz answered 503 forever, which
// takes an instance out of rotation but never restarts it. The result was a
// process that looked alive to its supervisor and could not do anything until
// a human restarted it.
//
// Two minutes rides out the ordinary case — the database and the gateway
// started together and the database is still recovering — and is short enough
// that a database that is really gone ends in an exit and a restart with
// backoff, which is what a supervisor is for and what an operator watching
// restarts can see.
const (
	bootConnectBudget   = 2 * time.Minute
	bootConnectMinDelay = time.Second
	bootConnectMaxDelay = 15 * time.Second
)

type bootRetry struct {
	budget   time.Duration
	minDelay time.Duration
	maxDelay time.Duration

	// Injected so a test can run the schedule without waiting it out.
	now   func() time.Time
	sleep func(time.Duration)
}

func defaultBootRetry() bootRetry {
	return bootRetry{
		budget:   bootConnectBudget,
		minDelay: bootConnectMinDelay,
		maxDelay: bootConnectMaxDelay,
		now:      time.Now,
		sleep:    time.Sleep,
	}
}

// openConfiguredDatabase connects to and migrates the database a configured
// gateway was given, and installs it on conn. An error means the gateway must
// not serve, and main exits on it.
//
// A failed migration used to be logged and ignored, and the gateway went on to
// serve whatever schema it found: the old one, or one stopped partway through a
// step. Every query this build makes assumes the new one, so the failures that
// followed surfaced one request at a time, far from their cause. Exiting puts
// the cause in the last log line, and the supervisor's restart retries the
// migration, which is safe now that each step is transactional.
func openConfiguredDatabase(
	cfg db.Config,
	conn db.Connection,
	connect func(db.Config) (*sql.DB, error),
	migrateFn func(db.Connection, string) error,
	r bootRetry,
) error {
	sqlDB, err := connectWithRetry(cfg, connect, r)
	if err != nil {
		return fmt.Errorf("could not connect to the configured database: %w", err)
	}
	conn.SetDB(sqlDB)

	if err := migrateFn(conn, cfg.Type); err != nil {
		return fmt.Errorf("could not bring the database schema up to date: %w", err)
	}
	return nil
}

// connectWithRetry connects to the configured database, retrying with capped
// exponential backoff until the budget is spent.
//
// A configuration mistake is returned at once: an engine panmail does not
// support will not become supported by waiting, and spending two minutes on it
// only delays the error the operator needs to read.
func connectWithRetry(cfg db.Config, connect func(db.Config) (*sql.DB, error), r bootRetry) (*sql.DB, error) {
	start := r.now()
	delay := r.minDelay

	for attempt := 1; ; attempt++ {
		sqlDB, err := connect(cfg)
		if err == nil {
			if attempt > 1 {
				slog.Info("connected to the database", "attempts", attempt)
			}
			return sqlDB, nil
		}
		if errors.Is(err, db.ErrInvalidConfig) {
			return nil, err
		}

		elapsed := r.now().Sub(start)
		if elapsed+delay > r.budget {
			return nil, fmt.Errorf("database still unreachable after %d attempts over %v: %w",
				attempt, elapsed.Round(time.Second), err)
		}

		slog.Warn("database not reachable yet; retrying",
			"attempt", attempt, "retry_in", delay, "error", err)
		r.sleep(delay)
		delay = min(delay*2, r.maxDelay)
	}
}
