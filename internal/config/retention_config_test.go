package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// writeConfig points config at a file this test owns and fills it with body.
func writeConfig(t *testing.T, body string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "db_config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	SetConfigPath(path)
	t.Cleanup(func() { SetConfigPath("") })
}

// The upgrade path. Every deployment that predates per-class retention has a
// config that looks like this, and the field it sets became a pointer.
func TestLoadReadsALegacyRetentionConfig(t *testing.T) {
	writeConfig(t, `
app:
  base_url: "https://mail.example.com"
  log_retention_days: 30
  retry_pattern:
    - 5m
    - 1h
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned no config")
	}

	if cfg.App.LogRetentionDays == nil {
		t.Fatal("log_retention_days did not survive the move to a pointer")
	}
	if got := *cfg.App.LogRetentionDays; got != 30 {
		t.Errorf("log_retention_days = %d; want 30", got)
	}

	// A field the old file never had reads as zero, which means keep forever —
	// so an upgrade deletes nothing that was not already being deleted.
	if cfg.App.WebhookRetentionDays != nil {
		t.Errorf("webhook_retention_days = %v; want it absent", *cfg.App.WebhookRetentionDays)
	}
	if cfg.App.MessageRetentionDays != 0 {
		t.Errorf("message_retention_days = %d; want 0", cfg.App.MessageRetentionDays)
	}
	if len(cfg.App.RetryPattern) != 2 {
		t.Errorf("retry pattern = %v; want both entries", cfg.App.RetryPattern)
	}
}

func TestLoadDistinguishesAnExplicitZeroFromAnAbsentField(t *testing.T) {
	writeConfig(t, `
app:
  retention_schema: 2
  log_retention_days: 0
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// This is the whole reason the field is a pointer: an operator who asked
	// to keep every event forever must not be given the 14-day default back.
	if cfg.App.LogRetentionDays == nil {
		t.Fatal("an explicit zero read as an absent field")
	}
	if got := *cfg.App.LogRetentionDays; got != 0 {
		t.Errorf("log_retention_days = %d; want the configured 0", got)
	}
}

// The upgrade that this marker exists for.
//
// The old field was a plain int written on every save whether or not anyone
// had configured it, so every deployment that went through the setup wizard
// has this exact file. Its zero meant "use the default"; read with the new
// rule it would mean "keep forever", quietly turning off the one retention
// panmail has always enforced.
func TestLoadTreatsAPreUpgradeZeroAsUnset(t *testing.T) {
	writeConfig(t, `
app:
  base_url: "https://mail.example.com"
  log_retention_days: 0
  outbox_retention_days: 0
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.LogRetentionDays != nil {
		t.Errorf("log_retention_days = %d; want it dropped so the default applies",
			*cfg.App.LogRetentionDays)
	}
}

func TestLoadKeepsAPreUpgradeNonZero(t *testing.T) {
	writeConfig(t, `
app:
  log_retention_days: 30
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Only the zero changed meaning. Thirty days meant thirty days before the
	// upgrade and still does.
	if cfg.App.LogRetentionDays == nil || *cfg.App.LogRetentionDays != 30 {
		t.Errorf("log_retention_days = %v; want 30", cfg.App.LogRetentionDays)
	}
}

func TestSaveStampsTheSchemaSoAChosenZeroSurvives(t *testing.T) {
	writeConfig(t, "app:\n  log_retention_days: 0\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// An administrator now chooses "keep forever" in the settings page.
	forever := 0
	cfg.App.LogRetentionDays = &forever
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if reloaded.App.LogRetentionDays == nil {
		t.Fatal("the chosen zero was migrated away as though it were the old default")
	}
	if got := *reloaded.App.LogRetentionDays; got != 0 {
		t.Errorf("log_retention_days = %d; want the chosen 0", got)
	}
}

func TestSaveOmitsRetentionsThatWereNeverSet(t *testing.T) {
	writeConfig(t, "app:\n  base_url: \"https://mail.example.com\"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path, err := GetConfigPath()
	if err != nil {
		t.Fatalf("GetConfigPath: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read the saved config: %v", err)
	}

	// Writing them as zero would turn "not configured, use the default" into
	// "keep forever" on the next read — a rewrite of the policy by a save that
	// touched something else entirely.
	for _, field := range []string{"log_retention_days", "webhook_retention_days"} {
		// Anchored to the start of a line: app_log_retention_days contains
		// log_retention_days as a substring, and a plain Contains here reports
		// a failure for a field that was written correctly.
		key := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(field) + `:`)
		if key.MatchString(string(written)) {
			t.Errorf("%s was written despite never being set:\n%s", field, written)
		}
	}
}
