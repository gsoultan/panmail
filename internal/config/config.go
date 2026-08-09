package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsoultan/panmail/pkg/db"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Database db.Config     `yaml:"database"`
	Auth     AuthConfig    `yaml:"auth"`
	App      AppConfig     `yaml:"app"`
	Secrets  SecretsConfig `yaml:"secrets"`
}

type AuthConfig struct {
	SymmetricKey string `yaml:"symmetric_key"` // 32 bytes hex encoded for Paseto v2
}

// SecretsConfig holds the key used to encrypt stored credentials.
//
// It is deliberately separate from AuthConfig.SymmetricKey: reusing the token
// signing key meant that rotating it would make every stored provider password
// undecryptable, and that compromising either one compromised both. Prefer
// supplying it through the PANMAIL_SECRET_KEY environment variable, which
// keeps it out of the config file entirely.
type SecretsConfig struct {
	DataKey string `yaml:"data_key,omitempty"`
}

type AppConfig struct {
	BaseURL          string `yaml:"base_url"`
	LogRetentionDays int    `yaml:"log_retention_days"`

	// How long a permanently failed message is kept before being pruned.
	// Failures are the only outbox rows that accumulate — a delivered message
	// is deleted outright — and each carries the whole serialised request,
	// body included, so without a cutoff this becomes the largest table in the
	// database holding nothing anyone will read. Zero disables pruning.
	OutboxRetentionDays int      `yaml:"outbox_retention_days"`
	RetryPattern        []string `yaml:"retry_pattern"`
}

var explicitConfigPath string

func SetConfigPath(path string) {
	explicitConfigPath = path
}

func GetConfigPath() (string, error) {
	if explicitConfigPath != "" {
		return explicitConfigPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".panmail", "db_config.yaml"), nil
}

func Load() (*Config, error) {
	path, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil // First run
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Decrypt database password if encrypted
	if cfg.Auth.SymmetricKey != "" && strings.HasPrefix(cfg.Database.Password, "enc:") {
		cipherText := strings.TrimPrefix(cfg.Database.Password, "enc:")
		plainText, err := decrypt(cipherText, cfg.Auth.SymmetricKey)
		if err != nil {
			// Leaving the "enc:" blob in place would surface later as a
			// confusing database authentication failure.
			return nil, fmt.Errorf("failed to decrypt database password: %w", err)
		}
		cfg.Database.Password = plainText
	}

	return &cfg, nil
}

func Save(cfg *Config) error {
	path, err := GetConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	// The directory holds the signing key and the encrypted database password,
	// so it is owner-only.
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Create a copy to encrypt the password without modifying the original
	cfgCopy := *cfg
	if cfgCopy.Auth.SymmetricKey != "" && cfgCopy.Database.Password != "" && !strings.HasPrefix(cfgCopy.Database.Password, "enc:") {
		encrypted, err := encrypt(cfgCopy.Database.Password, cfgCopy.Auth.SymmetricKey)
		if err != nil {
			// Writing the file anyway would silently store the password in
			// clear text, which is worse than refusing to save.
			return fmt.Errorf("failed to encrypt database password: %w", err)
		}
		cfgCopy.Database.Password = "enc:" + encrypted
	}

	data, err := yaml.Marshal(&cfgCopy)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

func encrypt(plainText, keyHex string) (string, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	cipherText := gcm.Seal(nonce, nonce, []byte(plainText), nil)
	return base64.StdEncoding.EncodeToString(cipherText), nil
}

func decrypt(cipherTextBase64, keyHex string) (string, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(cipherTextBase64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, cipherText := data[:nonceSize], data[nonceSize:]
	plainText, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return "", err
	}
	return string(plainText), nil
}
