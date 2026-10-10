package db

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

func openSQLite(t *testing.T, name string) *sql.DB {
	t.Helper()
	d, err := Connect(Config{Type: "sqlite", FilePath: filepath.Join(t.TempDir(), name)})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// First-run setup installs the database from a request handler while the
// workers started at boot are already reading it. Run under -race, this is the
// shape of that: one writer, several readers, no other synchronisation.
func TestTheHandleCanBeInstalledWhileWorkersReadIt(t *testing.T) {
	conn := NewConnection(nil)
	installed := openSQLite(t, "race.db")

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 1000 {
				if conn.IsConnected() {
					_ = conn.GetDB()
				}
			}
		})
	}
	wg.Go(func() { conn.SetDB(installed) })
	wg.Wait()

	if conn.GetDB() != installed {
		t.Error("the installed handle is not the one read back")
	}
}

// Setup can be retried: a failed migration leaves the operator to submit the
// form again, and each attempt connects afresh. The handle each attempt
// replaced used to stay open — a whole pool per retry.
func TestReplacingTheHandleClosesTheOldOne(t *testing.T) {
	tests := []struct {
		name        string
		replaceSame bool
	}{
		{"a different handle closes the old one", false},
		{"re-installing the same handle keeps it open", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first := openSQLite(t, "first.db")
			conn := NewConnection(first)

			next := first
			if !tc.replaceSame {
				next = openSQLite(t, "second.db")
			}
			conn.SetDB(next)

			err := first.Ping()
			if tc.replaceSame && err != nil {
				t.Errorf("the handle still in use was closed: %v", err)
			}
			if !tc.replaceSame && err == nil {
				t.Error("the replaced handle is still open; every retried setup leaks a pool")
			}
			if err := conn.GetDB().Ping(); err != nil {
				t.Errorf("the installed handle does not work: %v", err)
			}
		})
	}
}
