// Package repositories stores the SMTP submission listener's configuration.
package repositories

import (
	"context"

	"github.com/gsoultan/panmail/internal/smtp_submission/entities"
)

// ConfigRepository stores the one row describing the listener.
//
// Implementations encrypt the private key on the way in and decrypt it on the
// way out, so nothing above this interface handles ciphertext and nothing below
// it handles a key in the clear.
type ConfigRepository interface {
	// Get returns the stored configuration, or nil when nothing has been
	// stored. Nil is a first run, not an error: the caller applies defaults.
	Get(ctx context.Context) (*entities.Config, error)

	// Save writes the configuration, creating the row if it is absent.
	Save(ctx context.Context, cfg *entities.Config) error
}
