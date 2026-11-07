package usecases

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"golang.org/x/crypto/bcrypt"
)

func resetFixture() (*stubUserRepo, *entities.User) {
	old, _ := bcrypt.GenerateFromPassword([]byte("the-old-password"), bcrypt.MinCost)
	u := &entities.User{
		ID:               "u1",
		Email:            "admin@example.com",
		Password:         string(old),
		Role:             entities.RoleSuperAdmin,
		TwoFactorEnabled: true,
		TwoFactorSecret:  "SECRET",
	}
	return newStubUserRepo(u), u
}

func TestResetPasswordStoresAHashThatSignsIn(t *testing.T) {
	repo, u := resetFixture()

	_, err := ResetPassword(context.Background(), repo, PasswordReset{Email: " admin@example.com ", NewPassword: testPassword})
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if u.Password == testPassword {
		t.Fatal("the plaintext password was stored")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(testPassword)); err != nil {
		t.Fatalf("stored hash does not match the new password: %v", err)
	}
	if !u.TwoFactorEnabled || u.TwoFactorSecret != "SECRET" || repo.updateCalls != 0 {
		t.Fatal("two-factor was changed without being asked")
	}
}

func TestResetPasswordCanDisableTwoFactor(t *testing.T) {
	repo, u := resetFixture()

	_, err := ResetPassword(context.Background(), repo, PasswordReset{Email: u.Email, NewPassword: testPassword, DisableTwoFactor: true})
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if u.TwoFactorEnabled || u.TwoFactorSecret != "" {
		t.Fatalf("two-factor still on: enabled=%v secret=%q", u.TwoFactorEnabled, u.TwoFactorSecret)
	}
}

func TestResetPasswordRefusesAShortPassword(t *testing.T) {
	repo, u := resetFixture()
	before := u.Password

	_, err := ResetPassword(context.Background(), repo, PasswordReset{Email: u.Email, NewPassword: "short"})
	if !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("err = %v, want ErrPasswordTooShort", err)
	}
	if u.Password != before {
		t.Fatal("password changed despite the refusal")
	}
}

func TestResetPasswordReportsAnUnknownEmail(t *testing.T) {
	repo, _ := resetFixture()
	repo.byEmail = map[string]*entities.User{}

	_, err := ResetPassword(context.Background(), repo, PasswordReset{Email: "nobody@example.com", NewPassword: testPassword})
	if err == nil {
		t.Fatal("expected an error for an unknown email")
	}
}
