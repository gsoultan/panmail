package usecases

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	"golang.org/x/crypto/bcrypt"
)

// PasswordReset describes an operator's reset of one account's password.
type PasswordReset struct {
	Email       string
	NewPassword string
	// DisableTwoFactor also turns off the account's second factor, for the
	// case where the authenticator was lost along with the password.
	DisableTwoFactor bool
}

// ErrResetUserNotFound is returned when no account has the given email.
var ErrResetUserNotFound = errors.New("no user has that email address")

// ResetPassword sets a new password on the account with the given email,
// without the old one.
//
// It is for an operator with direct access to the database — the
// reset-password command — and deliberately has no API route: anyone who can
// reach it already holds the database credentials, so it grants nothing they
// could not do by hand, and it spares them hand-writing a bcrypt hash into
// SQL. The password rule and hashing are the same as every other way an
// account gets a password.
func ResetPassword(ctx context.Context, repo repositories.UserRepository, req PasswordReset) (*entities.User, error) {
	email := strings.TrimSpace(req.Email)
	if email == "" {
		return nil, errors.New("an email address is required")
	}
	if err := ValidatePassword(req.NewPassword); err != nil {
		return nil, err
	}

	user, err := repo.GetByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && user == nil) {
		return nil, ErrResetUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("look up user: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	if err := repo.UpdatePassword(ctx, user.ID, string(hash)); err != nil {
		return nil, fmt.Errorf("store new password: %w", err)
	}
	user.Password = string(hash)

	if req.DisableTwoFactor && user.TwoFactorEnabled {
		// The secret goes too: re-enabling later must enrol a new
		// authenticator rather than revive the lost one.
		if err := repo.UpdateTwoFactor(ctx, user.ID, false, ""); err != nil {
			return nil, fmt.Errorf("password was changed, but two-factor could not be disabled: %w", err)
		}
		user.TwoFactorEnabled = false
		user.TwoFactorSecret = ""
	}
	return user, nil
}
