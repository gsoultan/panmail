package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cockroachdb/pebble"
	pkgdb "github.com/gsoultan/panmail/pkg/db"
	_ "modernc.org/sqlite"
)

// A backup nobody has restored is a hypothesis.
//
// Everything else in this package checks the bookkeeping: that the manifest
// lists what was taken, that a failed snapshot fails the run, that an existing
// backup is not written over. None of it opens the output, so none of it
// answers the only question that matters on the day it is needed — does what
// came out work.
//
// These tests take a backup of live stores and then read it back through the
// same APIs a restore would: Pebble opening the checkpoint directory, and
// database/sql opening the snapshot file.

// pebbleStore adapts *pebble.DB to Checkpointer the same way the real stores
// do: Pebble's own method is variadic, so it does not satisfy the interface
// directly.
type pebbleStore struct{ *pebble.DB }

func (p pebbleStore) Checkpoint(dir string) error { return p.DB.Checkpoint(dir) }

func livePebble(t *testing.T, keys map[string]string) *pebble.DB {
	t.Helper()

	db, err := pebble.Open(filepath.Join(t.TempDir(), "live.db"), &pebble.Options{})
	if err != nil {
		t.Fatalf("open pebble: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for k, v := range keys {
		if err := db.Set([]byte(k), []byte(v), pebble.Sync); err != nil {
			t.Fatalf("write %s: %v", k, err)
		}
	}
	return db
}

func liveSQLite(t *testing.T, rows int) (*sql.DB, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "live.sqlite")

	// Through pkg/db rather than sql.Open, so this carries the pragmas a
	// gateway actually runs with — WAL and a busy timeout. Opening the file
	// bare makes the test both harsher than production and wrong about it: a
	// raw connection fails the snapshot with SQLITE_BUSY under any concurrent
	// write at all, which says nothing about whether panmail's backup works.
	db, err := pkgdb.Connect(pkgdb.Config{Type: "sqlite", FilePath: path})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`CREATE TABLE tenants (id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	// The table the churn writer targets during a backup. Without it every one
	// of those inserts fails, and a test that means to snapshot a database
	// under write load quietly snapshots an idle one.
	if _, err := db.Exec(`CREATE TABLE churn_marker (n INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create churn table: %v", err)
	}
	for i := range rows {
		if _, err := db.Exec(`INSERT INTO tenants VALUES (?, ?)`,
			fmt.Sprintf("tenant-%03d", i), fmt.Sprintf("Tenant %d", i)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return db, path
}

// The whole premise of this package is that copying a live store is not a
// backup. This is the claim under test: take one while both stores are being
// written to, then open it.
func TestABackupOfALiveSystemRestores(t *testing.T) {
	const pebbleKeys = 200
	const sqlRows = 200

	seed := map[string]string{}
	for i := range pebbleKeys {
		seed[fmt.Sprintf("msg_events:tenant-a:%04d", i)] = fmt.Sprintf("delivered-%04d", i)
	}
	events := livePebble(t, seed)
	sqlDB, sqlPath := liveSQLite(t, sqlRows)

	// Keep writing for the duration of the backup. A snapshot taken under load
	// is the only kind that gets taken in production, and it is the one that
	// exposes a memtable that was never flushed or a WAL that was never
	// checkpointed.
	stop := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(2)
	go func() {
		defer writers.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = events.Set([]byte(fmt.Sprintf("churn:%06d", i)), []byte("x"), pebble.NoSync)
		}
	}()
	var sqlWrites, sqlErrs int64
	go func() {
		defer writers.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := sqlDB.Exec(`INSERT OR REPLACE INTO churn_marker VALUES (?)`, i); err != nil {
				atomic.AddInt64(&sqlErrs, 1)
				continue
			}
			atomic.AddInt64(&sqlWrites, 1)
		}
	}()

	out := filepath.Join(t.TempDir(), "backup")
	manifest, err := Run(context.Background(), Options{
		OutDir:      out,
		SQLDB:       sqlDB,
		SQLEngine:   "sqlite",
		SQLitePath:  sqlPath,
		Stores:      map[string]Checkpointer{"events.db": pebbleStore{events}},
		SecretKeyID: "f29d0772",
		Version:     "test",
	})
	close(stop)
	writers.Wait()

	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	// Without this the test still passes while proving nothing: an earlier
	// version wrote to a table that did not exist, every insert failed, and
	// what it actually snapshotted was an idle database.
	if w := atomic.LoadInt64(&sqlWrites); w == 0 {
		t.Fatalf("no writes landed during the backup (%d failed); "+
			"this snapshotted an idle database", atomic.LoadInt64(&sqlErrs))
	} else {
		t.Logf("backup taken across %d concurrent database writes", w)
	}

	// The restore. Nothing below touches the live stores.
	t.Run("the pebble checkpoint opens and holds what was written", func(t *testing.T) {
		restored, err := pebble.Open(filepath.Join(out, "events.db"), &pebble.Options{ReadOnly: true})
		if err != nil {
			t.Fatalf("the checkpoint does not open as a pebble store: %v", err)
		}
		defer restored.Close()

		for k, want := range seed {
			got, closer, err := restored.Get([]byte(k))
			if err != nil {
				t.Fatalf("%s is missing from the restored store: %v", k, err)
			}
			if string(got) != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
			_ = closer.Close()
		}
	})

	t.Run("the sqlite snapshot opens and holds every row", func(t *testing.T) {
		restored, err := sql.Open("sqlite", filepath.Join(out, "panmail.sqlite"))
		if err != nil {
			t.Fatalf("open the snapshot: %v", err)
		}
		defer restored.Close()

		var count int
		if err := restored.QueryRow(`SELECT count(*) FROM tenants`).Scan(&count); err != nil {
			t.Fatalf("the snapshot does not read as a database: %v", err)
		}
		if count != sqlRows {
			t.Errorf("restored %d tenants, want %d", count, sqlRows)
		}

		// Spot-check a value, because a row count survives a truncated page.
		var name string
		if err := restored.QueryRow(`SELECT name FROM tenants WHERE id = ?`, "tenant-042").Scan(&name); err != nil {
			t.Fatalf("read tenant-042: %v", err)
		}
		if name != "Tenant 42" {
			t.Errorf("tenant-042 = %q, want %q", name, "Tenant 42")
		}
	})

	t.Run("the manifest says which key it needs", func(t *testing.T) {
		body, err := os.ReadFile(filepath.Join(out, "manifest.json"))
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		var m Manifest
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("parse manifest: %v", err)
		}
		// Restoring the data without the key that encrypted it leaves every
		// provider password and webhook secret unreadable, and the failure
		// surfaces far from the cause. The fingerprint is what lets a restore
		// say "this needs key f29d0772" up front.
		if m.SecretKeyID != manifest.SecretKeyID {
			t.Errorf("manifest key id = %q, want %q", m.SecretKeyID, manifest.SecretKeyID)
		}
		if m.SecretKeyID == "" {
			t.Error("the backup does not record which key its secrets are under")
		}
	})
}

// The live store must be usable afterwards. A backup that quiesces the thing it
// is backing up is an outage on a schedule.
func TestTakingABackupLeavesTheLiveStoreWritable(t *testing.T) {
	events := livePebble(t, map[string]string{"before": "1"})
	sqlDB, sqlPath := liveSQLite(t, 1)

	out := filepath.Join(t.TempDir(), "backup")
	if _, err := Run(context.Background(), Options{
		OutDir: out, SQLDB: sqlDB, SQLEngine: "sqlite", SQLitePath: sqlPath,
		Stores: map[string]Checkpointer{"events.db": pebbleStore{events}},
	}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	if err := events.Set([]byte("after"), []byte("2"), pebble.Sync); err != nil {
		t.Errorf("the pebble store is not writable after a backup: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO tenants VALUES ('after', 'After')`); err != nil {
		t.Errorf("the database is not writable after a backup: %v", err)
	}
}

// A backup directory without a manifest is an interrupted one. The manifest is
// written last precisely so this is decidable, and a restore has to be able to
// tell rather than restoring half a snapshot.
func TestAnInterruptedBackupIsDistinguishable(t *testing.T) {
	events := livePebble(t, map[string]string{"k": "v"})

	out := filepath.Join(t.TempDir(), "backup")
	if _, err := Run(context.Background(), Options{
		OutDir: out, SQLEngine: "sqlite",
		Stores: map[string]Checkpointer{"events.db": pebbleStore{events}},
	}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// What an interrupted run leaves behind: the stores copied, no manifest.
	if err := os.Remove(filepath.Join(out, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(out, "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("setup failed")
	}
	// The store is present and looks complete, which is the trap.
	if _, err := os.Stat(filepath.Join(out, "events.db")); err != nil {
		t.Fatalf("the checkpoint should still be there: %v", err)
	}
}
