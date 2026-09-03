package repositories

import (
	"context"

	"github.com/gsoultan/panmail/internal/system_settings/entities"
)

// SettingsRepository stores the one row of global settings.
type SettingsRepository interface {
	// Get returns the stored settings, or nil when nothing has been stored
	// yet. Nil is a first run, not an error: the caller applies defaults.
	Get(ctx context.Context) (*entities.Settings, error)

	// Save writes the settings, creating the row if it is absent.
	Save(ctx context.Context, s *entities.Settings) error

	// Seed writes the settings only if no row exists, and reports whether it
	// wrote. It is how a deployment upgrading from the config file keeps what
	// it had configured, and it must never overwrite: every instance runs it
	// at startup, so on the second and later instances the right outcome is to
	// do nothing.
	Seed(ctx context.Context, s *entities.Settings) (bool, error)
}
