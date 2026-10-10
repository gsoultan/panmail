package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// strictStore refuses a destination that exists, as Pebble's Checkpoint does.
type strictStore struct{}

func (strictStore) Checkpoint(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("checkpoint: %s already exists", dir)
	}
	return os.MkdirAll(dir, 0o700)
}

// A backup that failed part-way can be retried into the same directory. The
// failed run left its snapshots behind, and the snapshot of a store refuses a
// destination that exists — so the retry failed on the leftovers of the first
// attempt, and the only way out was a new directory.
func TestAFailedBackupCanBeRetriedIntoTheSameDirectory(t *testing.T) {
	out := filepath.Join(t.TempDir(), "backup")
	config := filepath.Join(t.TempDir(), "config.yaml")
	opts := Options{
		OutDir: out, SQLEngine: "postgres",
		Stores:     map[string]Checkpointer{"events.db": strictStore{}},
		ConfigPath: config, // not there yet: the run fails after the snapshot
	}

	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("a backup with a missing config file succeeded")
	}
	if _, err := os.Stat(filepath.Join(out, "events.db")); err != nil {
		t.Fatalf("setup: the failed run should have left its snapshot: %v", err)
	}

	if err := os.WriteFile(config, []byte("auth: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !slices.Contains(m.Stores, "events.db") {
		t.Errorf("retry recorded %v; want the store in it", m.Stores)
	}
	if _, err := os.Stat(filepath.Join(out, inProgressMarker)); !os.IsNotExist(err) {
		t.Error("a finished backup still says it is in progress")
	}
}

// Clearing leftovers is only safe when this package wrote them. A directory
// that already holds something under a backup's names, with no sign of an
// unfinished run, may be a gateway's own data directory: refused, not cleared.
func TestSomethingNotLeftByABackupIsNeverCleared(t *testing.T) {
	out := t.TempDir()
	live := filepath.Join(out, "events.db")
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "MANIFEST-000001"), []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), Options{
		OutDir: out, SQLEngine: "postgres",
		Stores: map[string]Checkpointer{"events.db": strictStore{}},
	})
	if err == nil {
		t.Fatal("a backup wrote over a directory it did not create")
	}
	if _, err := os.Stat(filepath.Join(live, "MANIFEST-000001")); err != nil {
		t.Errorf("the existing store was touched: %v", err)
	}
}

// The archives directory holds the only copy of every event retention has
// expired, and a backup used to leave it out.
func TestArchivesAreBackedUp(t *testing.T) {
	src := filepath.Join(t.TempDir(), "archives")
	file := filepath.Join(src, "tenant-a", "archive_20260101_000000.jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"id":"e1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A link is not an archive, and is not followed.
	if err := os.Symlink("/etc/hostname", filepath.Join(src, "tenant-a", "elsewhere.jsonl")); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "backup")
	m, err := Run(context.Background(), Options{
		OutDir: out, SQLEngine: "postgres",
		Dirs: map[string]string{"archives": src, "absent": filepath.Join(t.TempDir(), "never-made")},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(out, "archives", "tenant-a", "archive_20260101_000000.jsonl"))
	if err != nil || string(body) != `{"id":"e1"}`+"\n" {
		t.Errorf("archive copied as %q, %v", body, err)
	}
	if _, err := os.Lstat(filepath.Join(out, "archives", "tenant-a", "elsewhere.jsonl")); !os.IsNotExist(err) {
		t.Error("a symlink in the archive directory was copied")
	}
	if !slices.Contains(m.Stores, "archives") {
		t.Errorf("manifest lists %v; want archives", m.Stores)
	}
	// A directory that does not exist yet is nothing to back up, not a failure.
	if slices.Contains(m.Stores, "absent") {
		t.Error("a missing directory was recorded as backed up")
	}
}
