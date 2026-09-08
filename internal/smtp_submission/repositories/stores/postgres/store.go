// Package postgres stores the SMTP submission listener's configuration.
package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	"github.com/gsoultan/panmail/internal/smtp_submission/entities"
	"github.com/gsoultan/panmail/internal/smtp_submission/repositories"
	"github.com/gsoultan/panmail/pkg/db"
	"github.com/gsoultan/panmail/pkg/secrets"
)

var (
	//go:embed sql/get_config.sql
	getConfigQuery string
	//go:embed sql/save_config.sql
	saveConfigQuery string
)

// ErrNoEncryptionKey is returned when a private key would have to be written in
// the clear.
//
// Refusing is the whole point. Every other stored credential in this gateway is
// sealed with the data key, and a TLS private key that fell back to plaintext
// because a key was missing would be the one secret in the database readable by
// anything that could read the database.
var ErrNoEncryptionKey = errors.New(
	"smtp submission: a TLS private key cannot be stored because no data encryption key is configured")

type store struct {
	conn    db.Connection
	keyring *secrets.Keyring
}

// NewStore builds the configuration store.
//
// A nil keyring is accepted rather than rejected here: a deployment with no
// data key can still read and write a listener that has no TLS material, and
// failing at construction would take the whole settings page down over a
// certificate nobody had tried to install.
func NewStore(conn db.Connection, keyring *secrets.Keyring) repositories.ConfigRepository {
	return &store{conn: conn, keyring: keyring}
}

func (s *store) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *store) Get(ctx context.Context) (*entities.Config, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, err
	}

	var (
		out       entities.Config
		bindScope string
		certPEM   sql.NullString
		keyPEM    sql.NullString
	)
	err = dbConn.QueryRowContext(ctx, getConfigQuery).Scan(
		&out.Enabled,
		&bindScope,
		&out.Port,
		&certPEM,
		&keyPEM,
		&out.AllowInsecureAuth,
		&out.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing stored yet. Not an error — the caller applies defaults.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the SMTP submission configuration: %w", err)
	}

	out.BindScope = entities.BindScope(bindScope)
	out.TLSCertPEM = certPEM.String

	plainKey, err := s.unseal(keyPEM.String)
	if err != nil {
		return nil, err
	}
	out.TLSKeyPEM = plainKey

	return &out, nil
}

func (s *store) Save(ctx context.Context, cfg *entities.Config) error {
	if cfg == nil {
		return errors.New("smtp submission: a configuration is required")
	}

	// Sealed before the connection is touched, so a deployment with no data key
	// is told that its certificate cannot be stored rather than that the
	// database is unreachable.
	sealedKey, err := s.seal(cfg.TLSKeyPEM)
	if err != nil {
		return err
	}

	dbConn, err := s.getDB()
	if err != nil {
		return err
	}

	_, err = dbConn.ExecContext(ctx, saveConfigQuery,
		cfg.Enabled,
		string(cfg.BindScope),
		cfg.Port,
		cfg.TLSCertPEM,
		sealedKey,
		cfg.AllowInsecureAuth,
		cfg.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save the SMTP submission configuration: %w", err)
	}
	return nil
}

// seal encrypts the private key, or refuses.
//
// An empty key is stored empty rather than as ciphertext of nothing, so "no
// certificate installed" stays distinguishable in the column itself.
func (s *store) seal(privateKey string) (string, error) {
	if privateKey == "" {
		return "", nil
	}
	if s.keyring == nil {
		return "", ErrNoEncryptionKey
	}

	sealed, err := s.keyring.Encrypt(privateKey)
	if err != nil {
		// Deliberately does not wrap anything derived from the key itself.
		return "", fmt.Errorf("failed to encrypt the SMTP TLS private key: %w", err)
	}
	return sealed, nil
}

// unseal decrypts a stored private key.
//
// A stored value with no key to read it is an error rather than an empty
// result: silently serving without TLS because the ciphertext could not be
// opened would downgrade a listener the operator configured to be encrypted.
func (s *store) unseal(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if s.keyring == nil {
		return "", ErrNoEncryptionKey
	}

	plain, err := s.keyring.Decrypt(stored)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt the SMTP TLS private key: %w", err)
	}
	return plain, nil
}
