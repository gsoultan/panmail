package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/db"
)

// On a fresh install there is no configuration at boot, so the endpoint is
// mounted with a nil one, and setup writes the file afterwards. The handler
// read the boot-time value and dereferenced nil: /admin/backup panicked on
// every request until the process was restarted.
func TestBackupFollowsTheConfigurationWrittenBySetup(t *testing.T) {
	dir := t.TempDir()
	config.SetConfigPath(filepath.Join(dir, "config.yaml"))
	t.Cleanup(func() { config.SetConfigPath("") })

	conn := db.NewConnection(nil)
	mux := http.NewServeMux()
	// As main mounts it before setup: no configuration, no keyring, and stores
	// that are not under test here.
	mountBackupEndpoint(mux, conn, nil, nil, nil, nil, nil)

	post := func() (code int, body string) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				code, body = -1, fmt.Sprint(r)
			}
		}()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/backup?out="+filepath.Join(dir, "out"), nil))
		return rec.Code, rec.Body.String()
	}

	// Before setup there is nothing to back up, and saying so is the answer.
	if code, body := post(); code != http.StatusServiceUnavailable {
		t.Fatalf("before setup: status %d (%s), want 503", code, body)
	}

	// What setup does: write the file and install the database.
	dbPath := filepath.Join(dir, "panmail.db")
	cfg := &config.Config{
		Database: db.Config{Type: "sqlite", FilePath: dbPath},
		Auth:     config.AuthConfig{SymmetricKey: strings.Repeat("00", 32)},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	sqlDB, err := db.Connect(cfg.Database)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	conn.SetDB(sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	code, body := post()
	if code == -1 {
		t.Fatalf("the handler panicked after setup: %s", body)
	}
	// The stores passed here cannot snapshot, so the backup itself is refused
	// further on. What matters is that it got past the configuration.
	if code == http.StatusServiceUnavailable {
		t.Errorf("still reports not configured after setup: %s", body)
	}
	if !strings.Contains(body, "cannot snapshot itself") {
		t.Errorf("did not reach the backup after setup: %d %s", code, body)
	}
}
