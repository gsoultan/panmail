package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeStore struct {
	err     error
	dirMade string
}

func (f *fakeStore) Checkpoint(dir string) error {
	if f.err != nil {
		return f.err
	}
	f.dirMade = dir
	return os.MkdirAll(dir, 0o700)
}

func TestABackupRecordsEverythingItTook(t *testing.T) {
	out := filepath.Join(t.TempDir(), "backup")

	m, err := Run(context.Background(), Options{
		OutDir:      out,
		SQLEngine:   "sqlite",
		SecretKeyID: "f29d0772",
		Stores: map[string]Checkpointer{
			"events.db":  &fakeStore{},
			"inbound.db": &fakeStore{},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(m.Stores) != 2 {
		t.Errorf("recorded %v, want both stores", m.Stores)
	}
	// The fingerprint is what lets a restore say "this needs key f29d0772"
	// rather than failing later with a decryption error nobody can place.
	if m.SecretKeyID != "f29d0772" {
		t.Errorf("key id = %q, want it recorded", m.SecretKeyID)
	}
	if len(m.Notes) == 0 {
		t.Error("nothing said about needing the key to read this back")
	}
}

// A store that could not be snapshotted must fail the backup. Reporting
// success while containing a quarter of the data is the failure this command
// exists to prevent.
func TestAStoreThatCannotBeSnapshottedFailsTheBackup(t *testing.T) {
	out := filepath.Join(t.TempDir(), "backup")

	_, err := Run(context.Background(), Options{
		OutDir:    out,
		SQLEngine: "sqlite",
		Stores:    map[string]Checkpointer{"events.db": &fakeStore{err: errors.New("locked by another process")}},
	})
	if err == nil {
		t.Fatal("a failed checkpoint produced a successful backup")
	}
	// And no manifest, so nothing downstream mistakes it for a usable one.
	if _, statErr := os.Stat(filepath.Join(out, "manifest.json")); statErr == nil {
		t.Error("an incomplete backup was marked complete")
	}
}

// The manifest is written last, so its presence is the signal that the backup
// finished rather than that it started.
func TestTheManifestIsWrittenLastAndIsReadable(t *testing.T) {
	out := filepath.Join(t.TempDir(), "backup")

	if _, err := Run(context.Background(), Options{
		OutDir: out, SQLEngine: "sqlite",
		Stores: map[string]Checkpointer{"logs.db": &fakeStore{}},
	}); err != nil {
		t.Fatalf("run: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("manifest is not readable json: %v", err)
	}
	if m.TakenAt.IsZero() {
		t.Error("the manifest does not say when it was taken")
	}
}

// Writing a second backup over a first produces something that restores as
// neither.
func TestItRefusesToWriteOverAnExistingBackup(t *testing.T) {
	out := filepath.Join(t.TempDir(), "backup")
	opts := Options{OutDir: out, SQLEngine: "sqlite", Stores: map[string]Checkpointer{}}

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Error("a second backup overwrote the first")
	}
}

func TestPostgresIsLeftToItsOwnTooling(t *testing.T) {
	out := filepath.Join(t.TempDir(), "backup")

	m, err := Run(context.Background(), Options{
		OutDir: out, SQLEngine: "postgres",
		Stores: map[string]Checkpointer{},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Silence here would leave an operator believing the database was included.
	var mentioned bool
	for _, n := range m.Notes {
		if strings.Contains(n, "pg_dump") {
			mentioned = true
		}
	}
	if !mentioned {
		t.Errorf("notes %v do not say the database was not included", m.Notes)
	}
}

func TestAnOutputDirectoryIsRequired(t *testing.T) {
	if _, err := Run(context.Background(), Options{}); err == nil {
		t.Error("a backup with nowhere to go was accepted")
	}
}
