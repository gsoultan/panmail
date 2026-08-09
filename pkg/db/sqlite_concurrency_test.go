package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A gateway runs four background workers alongside its request handlers, all
// writing to the same database. On SQLite with the default settings the second
// writer to arrive does not queue — it fails immediately with SQLITE_BUSY, and
// for the outbox worker that means queued mail stops being sent for that tick:
//
//	failed to claim pending outbox emails: database is locked (5) (SQLITE_BUSY)
//
// These exercise Connect, not a hand-built DSN, because the defect was that
// production opened the file differently from every test harness.

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := Connect(Config{
		Type:     "sqlite",
		FilePath: filepath.Join(t.TempDir(), "concurrency.db"),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := d.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return d
}

func TestConcurrentWritersDoNotHitSqliteBusy(t *testing.T) {
	d := openTestDB(t)

	const writers, perWriter = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)

	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWriter {
				if _, err := d.Exec(`INSERT INTO t (v) VALUES (?)`, w*perWriter+i); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)

	var busy, other int
	var sample error
	for err := range errs {
		if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
			busy++
		} else {
			other++
		}
		if sample == nil {
			sample = err
		}
	}

	if busy > 0 {
		t.Errorf("%d writes failed with SQLITE_BUSY; queued mail would stop being sent", busy)
	}
	if other > 0 {
		t.Errorf("%d writes failed for another reason, first: %v", other, sample)
	}

	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if want := writers * perWriter; count != want {
		t.Errorf("%d rows written, want %d", count, want)
	}
}

// Under the default rollback journal a writer blocks every reader, so the
// dashboard stalls whenever the outbox drains. WAL is what stops that.
func TestReadsProceedDuringAWrite(t *testing.T) {
	d := openTestDB(t)

	var mode string
	if err := d.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal mode is %q, want wal — readers will block behind writers", mode)
	}
}

func TestBusyTimeoutIsSet(t *testing.T) {
	d := openTestDB(t)

	var timeout int
	if err := d.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if timeout <= 0 {
		t.Error("busy_timeout is zero, so a blocked writer fails instead of waiting")
	}
}

// Foreign keys are off by default in SQLite, which silently permits a row
// referencing a tenant that does not exist.
func TestForeignKeysAreEnforced(t *testing.T) {
	d := openTestDB(t)

	var on int
	if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if on != 1 {
		t.Error("foreign keys are not enforced; orphaned rows would be accepted")
	}
}

// An operator who supplies their own parameters must not have them mangled into
// a DSN with two query strings.
func TestAnExplicitDSNIsLeftAlone(t *testing.T) {
	custom := "/tmp/x.db?_pragma=busy_timeout(1000)"
	if got := sqliteDSN(custom); got != custom {
		t.Errorf("a caller-supplied DSN was rewritten: %q", got)
	}
	plain := "/tmp/x.db"
	if got := sqliteDSN(plain); !strings.Contains(got, "busy_timeout") {
		t.Errorf("a plain path did not gain the pragmas: %q", got)
	}
}
