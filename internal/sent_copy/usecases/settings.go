// Package usecases manages a tenant's Sent copies setting and files the
// copies it asks for.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/gsmail"

	"github.com/gsoultan/panmail/internal/sent_copy/entities"
	"github.com/gsoultan/panmail/internal/sent_copy/repositories"
)

// ErrEncryptionUnavailable is returned when a login mode is chosen on a
// gateway with no data key, which could not store the password it needs.
var ErrEncryptionUnavailable = errors.New(
	"sent copies: the login modes need a data encryption key, and this gateway has none")

// ErrMailboxNotFound is returned when a mailbox to delete does not exist.
var ErrMailboxNotFound = errors.New("sent copies: no mailbox login is stored for that address")

// Settings manages the setting and the mailbox logins. Every method is scoped
// to the tenant it is given; the caller takes that from the authenticated
// request, never from the request body.
type Settings struct {
	repo                repositories.Repository
	encryptionAvailable bool
	now                 func() time.Time
}

// NewSettings builds the settings usecase. encryptionAvailable says whether
// the store has a data key, so the login modes can be refused up front.
func NewSettings(repo repositories.Repository, encryptionAvailable bool) *Settings {
	return &Settings{repo: repo, encryptionAvailable: encryptionAvailable, now: time.Now}
}

// EncryptionAvailable reports whether passwords can be stored.
func (u *Settings) EncryptionAvailable() bool { return u.encryptionAvailable }

// Get returns the tenant's setting, or the default when none is stored.
func (u *Settings) Get(ctx context.Context, tenantID string) (*entities.Settings, error) {
	s, err := u.repo.GetSettings(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return entities.Default(tenantID), nil
	}
	return s, nil
}

// Update replaces the tenant's setting. An empty master password keeps the
// stored one, so an administrator can change the folder without retyping it.
func (u *Settings) Update(ctx context.Context, tenantID string, in *entities.Settings) (*entities.Settings, error) {
	next := *in
	next.TenantID = tenantID
	next.Normalise()

	if next.Mode == entities.ModeSharedLogin && next.MasterPassword == "" {
		current, err := u.repo.GetSettings(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		if current != nil && current.Mode == entities.ModeSharedLogin {
			next.MasterPassword = current.MasterPassword
		}
	}

	if err := next.Validate(); err != nil {
		return nil, err
	}
	if next.UsesIMAP() && !u.encryptionAvailable {
		return nil, ErrEncryptionUnavailable
	}

	next.UpdatedAt = u.now().UTC()
	if err := u.repo.SaveSettings(ctx, &next); err != nil {
		return nil, err
	}
	return &next, nil
}

// ListMailboxes returns the tenant's mailbox logins.
func (u *Settings) ListMailboxes(ctx context.Context, tenantID string) ([]*entities.Mailbox, error) {
	return u.repo.ListMailboxes(ctx, tenantID)
}

// SaveMailbox creates or replaces a mailbox login. An empty password keeps
// the stored one.
func (u *Settings) SaveMailbox(ctx context.Context, tenantID string, in *entities.Mailbox) (*entities.Mailbox, error) {
	if !u.encryptionAvailable {
		return nil, ErrEncryptionUnavailable
	}

	address, err := gsmail.ParseEmailAddress(in.Address)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", entities.ErrAddressRequired, err)
	}
	next := &entities.Mailbox{
		TenantID: tenantID,
		Address:  entities.NormaliseAddress(address.Address),
		Username: in.Username,
		Password: in.Password,
	}

	current, err := u.repo.GetMailbox(ctx, tenantID, next.Address)
	if err != nil {
		return nil, err
	}
	if next.Password == "" && current != nil {
		next.Password = current.Password
	}
	if err := entities.ValidateMailbox(next, current == nil); err != nil {
		return nil, err
	}

	next.UpdatedAt = u.now().UTC()
	if err := u.repo.SaveMailbox(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}

// DeleteMailbox removes a mailbox login.
func (u *Settings) DeleteMailbox(ctx context.Context, tenantID, address string) error {
	removed, err := u.repo.DeleteMailbox(ctx, tenantID, entities.NormaliseAddress(address))
	if err != nil {
		return err
	}
	if !removed {
		return ErrMailboxNotFound
	}
	return nil
}
