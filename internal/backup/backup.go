// Package backup makes a copy of everything a gateway would need to be rebuilt.
//
// The hard part is not copying files, it is copying them consistently. Both
// stores here are live while the backup runs: `cp` on an open Pebble directory
// captures memtables that have not been flushed and SSTables mid-compaction,
// producing a directory that opens but is missing writes; the same is true of a
// SQLite file being written to. Each store has a native way to take a coherent
// snapshot, and this uses those rather than the filesystem.
//
// The other hard part is the key. Every stored credential is encrypted, so a
// database restored without the key that wrote it is a database of unreadable
// secrets. The manifest records which key a backup needs — its short public
// fingerprint, never the key itself — so a restore can say "this needs key
// f29d0772" instead of failing later with a decryption error nobody can place.
package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Checkpointer is a store that can snapshot itself consistently.
type Checkpointer interface {
	Checkpoint(dir string) error
}

// Manifest describes what a backup contains and what is needed to read it.
type Manifest struct {
	TakenAt time.Time `json:"taken_at"`
	Version string    `json:"panmail_version"`

	// Database names the engine, so a restore does not try to load a SQLite
	// snapshot into PostgreSQL.
	Database string `json:"database_engine"`

	// SecretKeyID is the fingerprint of the key the stored credentials are
	// encrypted under. Not the key: this file sits next to the data it
	// describes, and a backup that carries its own key protects nothing.
	SecretKeyID string `json:"secret_key_id"`

	Stores []string `json:"stores"`

	// Notes carries anything an operator has to do by hand, so the
	// instructions travel with the backup rather than living in a wiki.
	Notes []string `json:"notes,omitempty"`
}

// Options describes what to copy and where.
type Options struct {
	OutDir string

	// SQLDB is the live connection. SQLite is snapshotted natively; any other
	// engine is left to its own tooling, which does the job better than this
	// could.
	SQLDB     *sql.DB
	SQLEngine string
	// SQLitePath is only used to name the output file.
	SQLitePath string

	// Stores maps a name to anything that can snapshot itself. An interface
	// rather than a *pebble.DB so the backup does not need to reach inside
	// each store for a handle it should not be holding.
	Stores map[string]Checkpointer

	// ConfigPath is copied verbatim: it carries the auth signing key, and on
	// installations that never set the environment variable, the data key too.
	ConfigPath string

	SecretKeyID string
	Version     string
}

// Run writes a backup into OutDir and returns the manifest it wrote.
func Run(ctx context.Context, opts Options) (*Manifest, error) {
	if opts.OutDir == "" {
		return nil, fmt.Errorf("an output directory is required")
	}
	// Refuse to write into a directory that already holds a backup rather than
	// mixing two of them, which produces something that restores as neither.
	if _, err := os.Stat(filepath.Join(opts.OutDir, "manifest.json")); err == nil {
		return nil, fmt.Errorf("%s already contains a backup; choose an empty directory", opts.OutDir)
	}
	if err := os.MkdirAll(opts.OutDir, 0o700); err != nil {
		return nil, err
	}

	manifest := &Manifest{
		TakenAt:     time.Now().UTC(),
		Version:     opts.Version,
		Database:    opts.SQLEngine,
		SecretKeyID: opts.SecretKeyID,
	}

	if err := backupSQL(ctx, opts, manifest); err != nil {
		return nil, err
	}

	// Pebble's Checkpoint writes a consistent snapshot of the whole store,
	// hard-linking what it can, so this stays cheap even for a large one.
	for name, store := range opts.Stores {
		if store == nil {
			continue
		}
		dest := filepath.Join(opts.OutDir, name)
		if err := store.Checkpoint(dest); err != nil {
			return nil, fmt.Errorf("checkpoint %s: %w", name, err)
		}
		manifest.Stores = append(manifest.Stores, name)
	}

	if opts.ConfigPath != "" {
		if err := copyFile(opts.ConfigPath, filepath.Join(opts.OutDir, "config.yaml")); err != nil {
			return nil, fmt.Errorf("copy config: %w", err)
		}
		manifest.Stores = append(manifest.Stores, "config.yaml")
		manifest.Notes = append(manifest.Notes,
			"config.yaml carries the auth signing key, and the data encryption key on installations that do not set PANMAIL_SECRET_KEY. Treat this backup as a secret.")
	}

	if manifest.SecretKeyID != "" {
		manifest.Notes = append(manifest.Notes,
			fmt.Sprintf("Stored credentials are encrypted under key %s. Restoring without it leaves every provider password, DKIM key and webhook secret unreadable.", manifest.SecretKeyID))
	}

	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	// Written last, so its presence means the backup finished. A reader can
	// then treat a directory without one as incomplete rather than restoring
	// half a snapshot.
	if err := os.WriteFile(filepath.Join(opts.OutDir, "manifest.json"), body, 0o600); err != nil {
		return nil, err
	}

	return manifest, nil
}

func backupSQL(ctx context.Context, opts Options, manifest *Manifest) error {
	// The engine decides this, not whether a handle happened to be passed:
	// silence here leaves an operator believing the database was included.
	if !strings.EqualFold(opts.SQLEngine, "sqlite") {
		// pg_dump understands roles, sequences and extensions; reimplementing
		// a worse version of it here would be a liability rather than a
		// convenience.
		manifest.Notes = append(manifest.Notes,
			"The SQL database is not included: run pg_dump against it and store the output alongside this directory.")
		return nil
	}

	if opts.SQLDB == nil {
		return nil
	}

	dest := filepath.Join(opts.OutDir, "panmail.sqlite")

	// VACUUM INTO takes a consistent snapshot of a live database, which
	// copying the file does not: a copy taken mid-write captures a torn page
	// or misses whatever is still in the WAL.
	if _, err := opts.SQLDB.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("snapshot sqlite: %w", err)
	}
	manifest.Stores = append(manifest.Stores, "panmail.sqlite")
	return nil
}

func copyFile(from, to string) error {
	body, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	// 0600: it holds keys.
	return os.WriteFile(to, body, 0o600)
}
