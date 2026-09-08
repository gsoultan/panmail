package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	tenantentities "github.com/gsoultan/panmail/internal/tenant/entities"
	tenantrepositories "github.com/gsoultan/panmail/internal/tenant/repositories"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionTokenTTL   = 24 * time.Hour
	challengeTokenTTL = 5 * time.Minute

	maxLoginAttempts = 5
	loginBlockPeriod = 15 * time.Minute

	// A TOTP code is only six digits, so the challenge step needs its own,
	// tighter budget than the password step.
	maxTwoFactorAttempts = 5
	twoFactorBlockPeriod = 15 * time.Minute

	defaultAdminRole = "USER_ROLE_SUPER_ADMIN"
	defaultTenant    = "Default Tenant"
)

// enumerationGuardHash is a valid bcrypt digest compared against when no user
// matches, so that an unknown address costs the same as a known one and cannot
// be distinguished by response time.
var enumerationGuardHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

// Credentials carries a sign-in attempt.
type Credentials struct {
	Email    string
	Password string
}

// NewAdmin carries the details of the first administrator.
type NewAdmin struct {
	Email    string
	Password string
	Name     string
}

// TwoFactorSetup is the enrolment material handed to a user who must configure
// an authenticator app.
type TwoFactorSetup struct {
	Secret    string
	QRCodeURL string
}

// SignInResult is the outcome of a sign-in or second-factor verification.
//
// Exactly one of Token or ChallengeToken is ever populated: Token grants API
// access, ChallengeToken only permits a second-factor attempt.
type SignInResult struct {
	User  *entities.User
	Token string

	TwoFactorRequired      bool
	TwoFactorSetupRequired bool
	ChallengeToken         string
	TwoFactorSetup         *TwoFactorSetup
}

type AuthUsecase interface {
	SignIn(ctx context.Context, creds Credentials) (*SignInResult, error)
	GetCurrentUser(ctx context.Context, userID string) (*entities.User, error)
	CreateAdmin(ctx context.Context, admin NewAdmin) error
	IsFirstRun(ctx context.Context) (bool, error)

	// 2FA methods
	SetupTwoFactor(ctx context.Context, userID string) (*TwoFactorSetup, error)
	VerifyTwoFactorLogin(ctx context.Context, challengeToken, code string) (*SignInResult, error)
	EnableTwoFactor(ctx context.Context, userID, code string) error
	DisableTwoFactor(ctx context.Context, userID string) error
}

type authUsecase struct {
	repo       repositories.UserRepository
	tenantRepo tenantrepositories.TenantRepository
	tokenMaker auth.TokenMaker

	// memberships records the first administrator's membership of the tenant
	// created alongside them. Nil is tolerated so that an instance wired
	// without it still completes setup.
	memberships repositories.MembershipRepository

	loginLimiter     *attemptLimiter
	twoFactorLimiter *attemptLimiter
	pendingTwoFactor *pendingTwoFactorStore
}

func NewAuthUsecase(
	repo repositories.UserRepository,
	tenantRepo tenantrepositories.TenantRepository,
	memberships repositories.MembershipRepository,
	tokenMaker auth.TokenMaker,
) AuthUsecase {
	return &authUsecase{
		repo:             repo,
		tenantRepo:       tenantRepo,
		memberships:      memberships,
		tokenMaker:       tokenMaker,
		loginLimiter:     newAttemptLimiter(maxLoginAttempts, loginBlockPeriod),
		twoFactorLimiter: newAttemptLimiter(maxTwoFactorAttempts, twoFactorBlockPeriod),
		pendingTwoFactor: newPendingTwoFactorStore(),
	}
}

func (u *authUsecase) SignIn(ctx context.Context, creds Credentials) (*SignInResult, error) {
	if blocked, retryIn := u.loginLimiter.Blocked(creds.Email); blocked {
		return nil, fmt.Errorf("too many login attempts. please try again in %d minutes", int(retryIn.Minutes())+1)
	}

	user, err := u.authenticatePassword(ctx, creds)
	if err != nil {
		u.loginLimiter.Fail(creds.Email)
		return nil, err
	}
	u.loginLimiter.Reset(creds.Email)

	if user.TwoFactorEnabled {
		return u.startTwoFactorChallenge(user)
	}

	token, err := u.issueSessionToken(user)
	if err != nil {
		return nil, err
	}
	return &SignInResult{User: user, Token: token}, nil
}

// authenticatePassword verifies the password, spending the same work on an
// unknown address as on a known one.
func (u *authUsecase) authenticatePassword(ctx context.Context, creds Credentials) (*entities.User, error) {
	invalid := errors.New("invalid email or password")

	user, err := u.repo.GetByEmail(ctx, creds.Email)
	if err != nil || user == nil {
		_ = bcrypt.CompareHashAndPassword(enumerationGuardHash, []byte(creds.Password))
		return nil, invalid
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(creds.Password)); err != nil {
		return nil, invalid
	}
	return user, nil
}

// startTwoFactorChallenge issues a short-lived challenge token. The token is
// the only thing that identifies the user during the second step, so a caller
// cannot target an arbitrary account by naming it.
func (u *authUsecase) startTwoFactorChallenge(user *entities.User) (*SignInResult, error) {
	challenge, err := u.tokenMaker.CreateToken(auth.TokenRequest{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
		Purpose:  auth.PurposeTwoFactorChallenge,
		Duration: challengeTokenTTL,
	})
	if err != nil {
		return nil, err
	}

	result := &SignInResult{User: user, ChallengeToken: challenge}

	// 2FA is required but the user has never enrolled: hand out fresh
	// enrolment material, held server-side until they confirm it.
	if user.TwoFactorSecret == "" {
		setup, err := u.SetupTwoFactor(context.Background(), user.ID)
		if err != nil {
			return nil, err
		}
		result.TwoFactorSetupRequired = true
		result.TwoFactorSetup = setup
		return result, nil
	}

	result.TwoFactorRequired = true
	return result, nil
}

func (u *authUsecase) issueSessionToken(user *entities.User) (string, error) {
	return u.tokenMaker.CreateToken(auth.TokenRequest{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
		Purpose:  auth.PurposeSession,
		Duration: sessionTokenTTL,
	})
}

func (u *authUsecase) GetCurrentUser(ctx context.Context, userID string) (*entities.User, error) {
	return u.repo.GetByID(ctx, userID)
}

func (u *authUsecase) CreateAdmin(ctx context.Context, admin NewAdmin) error {
	if err := validatePassword(admin.Password); err != nil {
		return err
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(admin.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	tenantID := uuid.New().String()
	tenant := &tenantentities.Tenant{
		ID:        tenantID,
		Name:      defaultTenant,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := u.tenantRepo.Create(ctx, tenant); err != nil {
		return err
	}

	user := &entities.User{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Email:     admin.Email,
		Password:  string(hashedPassword),
		Name:      admin.Name,
		Role:      defaultAdminRole,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := u.repo.Create(ctx, user); err != nil {
		return err
	}

	// The home tenant is a membership like any other, so that the first
	// administrator is listed among their own tenant's members and can be
	// assigned to further tenants by the same path as anybody else. A super
	// admin's global role is not a tenant role, so the row records
	// administrator, the strongest role that can be held locally.
	if u.memberships == nil {
		return nil
	}
	return u.memberships.Assign(ctx, &entities.UserTenant{
		UserID:    user.ID,
		TenantID:  tenantID,
		Role:      entities.RoleAdmin,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	})
}

func validatePassword(password string) error {
	const minPasswordLength = 12
	if len(password) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}
	return nil
}

func (u *authUsecase) IsFirstRun(ctx context.Context) (bool, error) {
	count, err := u.repo.Count(ctx)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

// SetupTwoFactor generates enrolment material and holds the secret server-side
// until the user proves possession by confirming a code.
func (u *authUsecase) SetupTwoFactor(ctx context.Context, userID string) (*TwoFactorSetup, error) {
	user, err := u.repo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, errors.New("user not found")
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Panmail",
		AccountName: user.Email,
	})
	if err != nil {
		return nil, err
	}

	u.pendingTwoFactor.Put(user.ID, key.Secret())

	return &TwoFactorSetup{Secret: key.Secret(), QRCodeURL: key.URL()}, nil
}

// VerifyTwoFactorLogin completes a sign-in that stopped at the second factor.
// The user is identified solely by the challenge token, and the secret is read
// from the user's own record (or from their pending enrolment) — never from
// the caller.
func (u *authUsecase) VerifyTwoFactorLogin(ctx context.Context, challengeToken, code string) (*SignInResult, error) {
	payload, err := u.tokenMaker.VerifyToken(challengeToken, auth.PurposeTwoFactorChallenge)
	if err != nil {
		return nil, errors.New("invalid or expired two-factor challenge")
	}

	if blocked, retryIn := u.twoFactorLimiter.Blocked(payload.UserID); blocked {
		return nil, fmt.Errorf("too many verification attempts. please try again in %d minutes", int(retryIn.Minutes())+1)
	}

	user, err := u.repo.GetByID(ctx, payload.UserID)
	if err != nil || user == nil {
		return nil, errors.New("invalid or expired two-factor challenge")
	}

	secret, enrolling := u.resolveTwoFactorSecret(user)
	if secret == "" {
		return nil, errors.New("two factor is not set up for this account")
	}

	if !totp.Validate(code, secret) {
		u.twoFactorLimiter.Fail(user.ID)
		return nil, errors.New("invalid verification code")
	}
	u.twoFactorLimiter.Reset(user.ID)

	if enrolling {
		if err := u.repo.UpdateTwoFactor(ctx, user.ID, true, secret); err != nil {
			return nil, err
		}
		u.pendingTwoFactor.Delete(user.ID)
		user.TwoFactorSecret = secret
		user.TwoFactorEnabled = true
	}

	token, err := u.issueSessionToken(user)
	if err != nil {
		return nil, err
	}

	return &SignInResult{User: user, Token: token}, nil
}

// resolveTwoFactorSecret returns the secret to validate against and whether it
// comes from an outstanding enrolment rather than the stored record.
func (u *authUsecase) resolveTwoFactorSecret(user *entities.User) (string, bool) {
	if user.TwoFactorSecret != "" {
		return user.TwoFactorSecret, false
	}
	if pending, ok := u.pendingTwoFactor.Get(user.ID); ok {
		return pending, true
	}
	return "", false
}

// EnableTwoFactor confirms an enrolment started by SetupTwoFactor. The secret
// is taken from the server-side pending store, so the caller can only confirm
// material this server issued to them.
func (u *authUsecase) EnableTwoFactor(ctx context.Context, userID, code string) error {
	if blocked, retryIn := u.twoFactorLimiter.Blocked(userID); blocked {
		return fmt.Errorf("too many verification attempts. please try again in %d minutes", int(retryIn.Minutes())+1)
	}

	secret, ok := u.pendingTwoFactor.Get(userID)
	if !ok {
		return errors.New("no pending two-factor setup: start enrolment again")
	}

	if !totp.Validate(code, secret) {
		u.twoFactorLimiter.Fail(userID)
		return errors.New("invalid verification code")
	}
	u.twoFactorLimiter.Reset(userID)

	if err := u.repo.UpdateTwoFactor(ctx, userID, true, secret); err != nil {
		return err
	}
	u.pendingTwoFactor.Delete(userID)
	return nil
}

func (u *authUsecase) DisableTwoFactor(ctx context.Context, userID string) error {
	u.pendingTwoFactor.Delete(userID)
	return u.repo.UpdateTwoFactor(ctx, userID, false, "")
}
