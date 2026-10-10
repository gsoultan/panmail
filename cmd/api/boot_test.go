package main

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsoultan/panmail/pkg/db"
)

// fakeClock lets the retry schedule run without anyone waiting for it.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) retry() bootRetry {
	return bootRetry{
		budget:   bootConnectBudget,
		minDelay: bootConnectMinDelay,
		maxDelay: bootConnectMaxDelay,
		now:      func() time.Time { return c.now },
		sleep: func(d time.Duration) {
			c.sleeps = append(c.sleeps, d)
			c.now = c.now.Add(d)
		},
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Connect(db.Config{Type: "sqlite", FilePath: filepath.Join(t.TempDir(), "boot.db")})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// A gateway whose database was unreachable at boot used to log it and carry on
// with no database, and never try again: /healthz 200, /readyz 503 forever,
// and nothing that would ever restart it.
func TestBootConnect(t *testing.T) {
	refused := errors.New("connect: connection refused")
	invalid := fmt.Errorf("unsupported: %w", db.ErrInvalidConfig)

	tests := []struct {
		name string
		// failures is how many attempts fail before one succeeds; -1 never.
		failures   int
		failWith   error
		wantErr    bool
		wantTries  int
		checkDelay func(t *testing.T, sleeps []time.Duration)
	}{
		{
			name: "reachable at once", failures: 0, wantTries: 1,
		},
		{
			name: "comes up while we wait", failures: 3, failWith: refused, wantTries: 4,
			checkDelay: func(t *testing.T, sleeps []time.Duration) {
				want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
				if fmt.Sprint(sleeps) != fmt.Sprint(want) {
					t.Errorf("waited %v, want backoff %v", sleeps, want)
				}
			},
		},
		{
			name: "never comes up: give up within the budget", failures: -1, failWith: refused, wantErr: true,
			checkDelay: func(t *testing.T, sleeps []time.Duration) {
				var total time.Duration
				for _, d := range sleeps {
					total += d
					if d > bootConnectMaxDelay {
						t.Errorf("waited %v in one go; the cap is %v", d, bootConnectMaxDelay)
					}
				}
				if total > bootConnectBudget {
					t.Errorf("waited %v in all; the budget is %v", total, bootConnectBudget)
				}
				if total < bootConnectBudget/2 {
					t.Errorf("gave up after %v; a database restarting alongside the gateway needs longer", total)
				}
			},
		},
		{
			name: "a configuration mistake is not retried", failures: -1, failWith: invalid, wantErr: true, wantTries: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			live := openTestDB(t)
			clock := &fakeClock{now: time.Unix(0, 0)}
			tries := 0
			connect := func(db.Config) (*sql.DB, error) {
				tries++
				if tc.failures < 0 || tries <= tc.failures {
					return nil, tc.failWith
				}
				return live, nil
			}

			got, err := connectWithRetry(db.Config{Type: "postgres"}, connect, clock.retry())
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected to give up")
				}
				if !errors.Is(err, tc.failWith) {
					t.Errorf("the last failure is not in the error an operator reads: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != live {
					t.Error("returned a different handle from the one that connected")
				}
			}
			if tc.wantTries != 0 && tries != tc.wantTries {
				t.Errorf("connected %d times, want %d", tries, tc.wantTries)
			}
			if tc.checkDelay != nil {
				tc.checkDelay(t, clock.sleeps)
			}
		})
	}
}

// A failed migration used to be logged and ignored, and the gateway served
// whatever schema it found. It is now an error main exits on.
func TestAFailedMigrationStopsTheBoot(t *testing.T) {
	tests := []struct {
		name       string
		migrateErr error
		wantErr    bool
	}{
		{"migrated", nil, false},
		{"migration failed", errors.New("migration 0019_x failed: syntax error"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			live := openTestDB(t)
			conn := db.NewConnection(nil)
			clock := &fakeClock{now: time.Unix(0, 0)}
			connect := func(db.Config) (*sql.DB, error) { return live, nil }
			migrated := false
			migrateFn := func(c db.Connection, _ string) error {
				migrated = c.GetDB() == live
				return tc.migrateErr
			}

			err := openConfiguredDatabase(db.Config{Type: "sqlite"}, conn, connect, migrateFn, clock.retry())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, tc.migrateErr) {
				t.Errorf("the migration's own error is lost: %v", err)
			}
			if !migrated {
				t.Error("the migration did not run against the handle that was connected")
			}
		})
	}
}
