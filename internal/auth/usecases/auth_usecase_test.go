package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	tenantentities "github.com/gsoultan/panmail/internal/tenant/entities"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const testPassword = "correct-horse-battery-staple"

type stubUserRepo struct {
	byEmail map[string]*entities.User
	byID    map[string]*entities.User

	updatedSecret  string
	updatedEnabled bool
	updateCalls    int
}

func newStubUserRepo(users ...*entities.User) *stubUserRepo {
	r := &stubUserRepo{
		byEmail: make(map[string]*entities.User),
		byID:    make(map[string]*entities.User),
	}
	for _, u := range users {
		r.byEmail[u.Email] = u
		r.byID[u.ID] = u
	}
	return r
}

func (r *stubUserRepo) Create(ctx context.Context, u *entities.User) error { return nil }
func (r *stubUserRepo) GetByEmail(ctx context.Context, email string) (*entities.User, error) {
	u, ok := r.byEmail[email]
	if !ok {
		return nil, errors.New("not found")
	}
	return u, nil
}
func (r *stubUserRepo) GetByID(ctx context.Context, id string) (*entities.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return u, nil
}
func (r *stubUserRepo) ListByTenantID(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.User, string, error) {
	return nil, "", nil
}
func (r *stubUserRepo) UpdateRole(ctx context.Context, id string, role string) error { return nil }
func (r *stubUserRepo) UpdateTwoFactor(ctx context.Context, id string, enabled bool, secret string) error {
	r.updateCalls++
	r.updatedEnabled = enabled
	r.updatedSecret = secret
	if u, ok := r.byID[id]; ok {
		u.TwoFactorEnabled = enabled
		u.TwoFactorSecret = secret
	}
	return nil
}
func (r *stubUserRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *stubUserRepo) Count(ctx context.Context) (int, error)      { return len(r.byID), nil }

type stubTenantRepo struct{}

func (r *stubTenantRepo) Create(ctx context.Context, t *tenantentities.Tenant) error { return nil }
func (r *stubTenantRepo) GetByID(ctx context.Context, id string) (*tenantentities.Tenant, error) {
	return nil, nil
}
func (r *stubTenantRepo) List(ctx context.Context, pageSize int, pageToken string) ([]*tenantentities.Tenant, string, error) {
	return nil, "", nil
}
func (r *stubTenantRepo) Update(ctx context.Context, t *tenantentities.Tenant) error { return nil }
func (r *stubTenantRepo) Delete(ctx context.Context, id string) error                { return nil }

func newTestUsecase(t *testing.T, repo *stubUserRepo) (AuthUsecase, auth.TokenMaker) {
	t.Helper()
	maker, err := auth.NewPasetoMaker("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("failed to build token maker: %v", err)
	}
	return NewAuthUsecase(repo, &stubTenantRepo{}, nil, maker), maker
}

func newUser(t *testing.T, secret string) *entities.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	return &entities.User{
		ID:               "victim-id",
		TenantID:         "tenant-1",
		Email:            "admin@example.com",
		Name:             "Admin",
		Password:         string(hash),
		Role:             "USER_ROLE_SUPER_ADMIN",
		TwoFactorEnabled: secret != "",
		TwoFactorSecret:  secret,
	}
}

func generateSecret(t *testing.T) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Panmail", AccountName: "test@example.com"})
	if err != nil {
		t.Fatalf("failed to generate secret: %v", err)
	}
	return key.Secret()
}

func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("failed to generate code: %v", err)
	}
	return code
}

// The bypass: a caller who knew only a victim's email could present a TOTP
// secret they controlled, have it validated against itself, overwrite the
// victim's secret, and receive a session token carrying the victim's role.
//
// The account is now identified solely by a challenge token that SignIn issues
// after a correct password, so there is no way in without one.
func TestVerifyTwoFactorLoginRequiresAChallengeToken(t *testing.T) {
	victimSecret := generateSecret(t)
	repo := newStubUserRepo(newUser(t, victimSecret))
	usecase, _ := newTestUsecase(t, repo)

	attackerSecret := generateSecret(t)

	tests := []struct {
		name           string
		challengeToken string
		code           string
	}{
		{"no challenge token at all", "", currentCode(t, attackerSecret)},
		{"forged challenge token", "v2.local.not-a-real-token", currentCode(t, attackerSecret)},
		{"attacker code against their own secret", "", currentCode(t, attackerSecret)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := usecase.VerifyTwoFactorLogin(context.Background(), tc.challengeToken, tc.code)
			if err == nil {
				t.Fatal("expected verification to be refused")
			}
			if result != nil {
				t.Error("no result should be returned on refusal")
			}
			if repo.updateCalls != 0 {
				t.Error("the victim's two-factor secret must never be written by a failed attempt")
			}
		})
	}

	if repo.byID["victim-id"].TwoFactorSecret != victimSecret {
		t.Error("the victim's secret was modified")
	}
	if repo.byID["victim-id"].TwoFactorSecret == attackerSecret {
		t.Fatal("the victim's secret was replaced with the attacker's")
	}
}

// A challenge token is bound to the account that passed the password step, so
// stealing one for account A cannot be used to complete a login as account B.
func TestVerifyTwoFactorLoginRejectsSessionTokenAsChallenge(t *testing.T) {
	secret := generateSecret(t)
	repo := newStubUserRepo(newUser(t, secret))
	usecase, maker := newTestUsecase(t, repo)

	session, err := maker.CreateToken(auth.TokenRequest{
		UserID:   "victim-id",
		TenantID: "tenant-1",
		Role:     "USER_ROLE_SUPER_ADMIN",
		Purpose:  auth.PurposeSession,
		Duration: time.Minute,
	})
	if err != nil {
		t.Fatalf("failed to mint session token: %v", err)
	}

	if _, err := usecase.VerifyTwoFactorLogin(context.Background(), session, currentCode(t, secret)); err == nil {
		t.Error("a session token must not be accepted as a two-factor challenge")
	}
}

func TestSignInIssuesChallengeNotSessionWhenTwoFactorEnabled(t *testing.T) {
	secret := generateSecret(t)
	repo := newStubUserRepo(newUser(t, secret))
	usecase, _ := newTestUsecase(t, repo)

	result, err := usecase.SignIn(context.Background(), Credentials{
		Email:    "admin@example.com",
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("sign in failed: %v", err)
	}

	if result.Token != "" {
		t.Error("no session token may be issued before the second factor")
	}
	if result.ChallengeToken == "" {
		t.Fatal("expected a challenge token")
	}
	if !result.TwoFactorRequired {
		t.Error("expected two factor to be flagged as required")
	}

	// The happy path completes with the account's own secret.
	final, err := usecase.VerifyTwoFactorLogin(context.Background(), result.ChallengeToken, currentCode(t, secret))
	if err != nil {
		t.Fatalf("verification failed: %v", err)
	}
	if final.Token == "" {
		t.Error("expected a session token after successful verification")
	}
}

func TestSignInRejectsWrongPassword(t *testing.T) {
	repo := newStubUserRepo(newUser(t, ""))
	usecase, _ := newTestUsecase(t, repo)

	tests := []struct {
		name  string
		creds Credentials
	}{
		{"wrong password", Credentials{Email: "admin@example.com", Password: "nope"}},
		{"unknown account", Credentials{Email: "nobody@example.com", Password: testPassword}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := usecase.SignIn(context.Background(), tc.creds); err == nil {
				t.Error("expected sign in to be refused")
			}
		})
	}
}

func TestSignInLocksOutAfterRepeatedFailures(t *testing.T) {
	repo := newStubUserRepo(newUser(t, ""))
	usecase, _ := newTestUsecase(t, repo)

	bad := Credentials{Email: "admin@example.com", Password: "wrong"}
	for range maxLoginAttempts {
		if _, err := usecase.SignIn(context.Background(), bad); err == nil {
			t.Fatal("expected failure")
		}
	}

	// The correct password is now refused too, because the account is locked.
	_, err := usecase.SignIn(context.Background(), Credentials{
		Email:    "admin@example.com",
		Password: testPassword,
	})
	if err == nil {
		t.Error("expected the account to be locked out")
	}
}

// EnableTwoFactor must confirm material this server issued, not whatever the
// caller supplies.
func TestEnableTwoFactorRequiresAPendingEnrolment(t *testing.T) {
	repo := newStubUserRepo(newUser(t, ""))
	usecase, _ := newTestUsecase(t, repo)

	foreignSecret := generateSecret(t)
	if err := usecase.EnableTwoFactor(context.Background(), "victim-id", currentCode(t, foreignSecret)); err == nil {
		t.Error("expected enrolment to be refused without a pending setup")
	}
	if repo.updateCalls != 0 {
		t.Error("no two-factor write should have happened")
	}

	setup, err := usecase.SetupTwoFactor(context.Background(), "victim-id")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := usecase.EnableTwoFactor(context.Background(), "victim-id", currentCode(t, setup.Secret)); err != nil {
		t.Fatalf("expected enrolment to succeed: %v", err)
	}
	if repo.updatedSecret != setup.Secret || !repo.updatedEnabled {
		t.Error("expected the server-issued secret to be persisted")
	}
}

func TestCreateAdminRejectsWeakPassword(t *testing.T) {
	repo := newStubUserRepo()
	usecase, _ := newTestUsecase(t, repo)

	if err := usecase.CreateAdmin(context.Background(), NewAdmin{
		Email:    "admin@example.com",
		Password: "short",
		Name:     "Admin",
	}); err == nil {
		t.Error("expected a weak password to be refused")
	}
}
