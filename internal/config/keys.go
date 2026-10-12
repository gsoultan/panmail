package config

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/gsoultan/panmail/pkg/secrets"
)

// ResolveDataKey returns the key used to encrypt stored credentials.
//
// The environment variable wins, so a deployment can keep the key out of the
// config file and out of backups of it. Otherwise the key recorded at setup is
// used, and a warning explains the trade-off: a key sitting beside the data it
// protects only helps against a stolen database, not a stolen disk.
func ResolveDataKey(cfg *Config) (string, error) {
	if fromEnv := os.Getenv(secrets.EnvKeyName); fromEnv != "" {
		return fromEnv, nil
	}

	if cfg != nil && cfg.Secrets.DataKey != "" {
		slog.Warn("using the data encryption key stored in the configuration file",
			"hint", fmt.Sprintf("set %s to keep it out of the config file", secrets.EnvKeyName))
		return cfg.Secrets.DataKey, nil
	}

	return "", secrets.ErrNoKey
}

// RetiredDataKeys returns the decrypt-only keys: those named in
// PANMAIL_SECRET_KEYS_RETIRED, then any an interrupted key change left in the
// config file.
func RetiredDataKeys(cfg *Config) []string {
	retired := secrets.ParseRetiredKeys(os.Getenv(secrets.EnvRetiredKeysName))
	if cfg != nil {
		retired = append(retired, cfg.Secrets.RetiredDataKeys...)
	}
	return retired
}

// EnsureDataKey returns the configured key, generating and persisting one if
// this instance has never had one. Called during setup and at start-up so an
// existing installation gains encryption without operator action.
func EnsureDataKey(cfg *Config) (string, error) {
	if key, err := ResolveDataKey(cfg); err == nil {
		return key, nil
	}

	key, err := secrets.GenerateKey()
	if err != nil {
		return "", fmt.Errorf("failed to generate a data encryption key: %w", err)
	}

	cfg.Secrets.DataKey = key
	if err := Save(cfg); err != nil {
		return "", fmt.Errorf("failed to persist the data encryption key: %w", err)
	}

	slog.Info("generated a data encryption key for stored credentials",
		"hint", fmt.Sprintf("back up %s; without it stored provider passwords cannot be read", mustPath()))

	return key, nil
}

// FirstRunDataKey returns the key an instance that has not been set up yet
// encrypts with, and the value setup has to persist so a restart reads the same
// key back. toPersist is empty when the key comes from the environment, which
// is never copied into the config file.
//
// The key has to exist from the first boot, not from the first restart. The
// setup wizard brings the instance up without a restart, and everything built
// at boot keeps the keyring it was given: before this, the first provider saved
// after setup failed with "no encryption key configured" until a restart.
func FirstRunDataKey() (key, toPersist string, err error) {
	if key, err := ResolveDataKey(nil); err == nil {
		return key, "", nil
	}

	key, err = secrets.GenerateKey()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate a data encryption key: %w", err)
	}
	return key, key, nil
}

func mustPath() string {
	path, err := GetConfigPath()
	if err != nil {
		return "the configuration file"
	}
	return path
}
