package usecases

import (
	"context"
	"path/filepath"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/gsoultan/panmail/pkg/db"
	"github.com/gsoultan/panmail/pkg/secrets"
)

// The process encrypts credentials from the moment setup finishes, with a key
// chosen at boot. Setup has to write that same key, or the first restart reads
// a different one — or generates a new one — and every provider password saved
// in between is unreadable.
func TestSetupPersistsTheKeyTheProcessEncryptsWith(t *testing.T) {
	dir := t.TempDir()
	config.SetConfigPath(filepath.Join(dir, "db_config.yaml"))
	t.Cleanup(func() { config.SetConfigPath("") })
	t.Setenv(secrets.EnvKeyName, "")

	key, toPersist, err := config.FirstRunDataKey()
	if err != nil {
		t.Fatal(err)
	}
	if toPersist != key {
		t.Fatal("a generated key must be handed to setup to persist")
	}
	running, err := secrets.NewKeyring(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := running.Encrypt("smtp-password")
	if err != nil {
		t.Fatal(err)
	}

	u := NewSetupUsecase(
		&mockAuthUsecase{isFirstRun: true},
		&mockConnection{},
		auth.NewSwappableTokenMaker(nil),
		nil,
		toPersist,
		func(db.Connection, string) error { return nil },
	)
	err = u.Setup(context.Background(), &panmailv1.DatabaseConfig{
		Type:     "sqlite",
		FilePath: filepath.Join(dir, "panmail.db"),
	}, "admin@example.com", "a-long-enough-password", "Admin", "https://mail.example.com")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	saved, err := config.Load()
	if err != nil || saved == nil {
		t.Fatalf("could not read back the config setup wrote: %v", err)
	}
	afterRestart, err := config.ResolveDataKey(saved)
	if err != nil {
		t.Fatalf("a restart finds no data key in the config setup wrote: %v", err)
	}
	restarted, err := secrets.NewKeyring(afterRestart)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.Decrypt(sealed); err != nil || got != "smtp-password" {
		t.Fatalf("a credential saved right after setup is unreadable after a restart: %q, %v", got, err)
	}
}

// A key supplied through the environment is the operator keeping it out of the
// config file; setup must not copy it there.
func TestFirstRunDataKeyLeavesAnEnvironmentKeyOutOfTheConfig(t *testing.T) {
	envKey, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(secrets.EnvKeyName, envKey)

	key, toPersist, err := config.FirstRunDataKey()
	if err != nil {
		t.Fatal(err)
	}
	if key != envKey {
		t.Fatalf("got key %q, want the one from %s", key, secrets.EnvKeyName)
	}
	if toPersist != "" {
		t.Fatal("the environment key would be written to the config file")
	}
}
