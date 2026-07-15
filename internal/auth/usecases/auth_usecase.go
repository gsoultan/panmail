package usecases

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

type AuthUsecase interface {
	SignIn(ctx context.Context, email, password string) (*entities.User, string, bool, bool, string, string, error)
	GetCurrentUser(ctx context.Context, userID string) (*entities.User, error)
	CreateAdmin(ctx context.Context, email, password, name string) error
	IsFirstRun(ctx context.Context) (bool, error)

	// 2FA methods
	SetupTwoFactor(ctx context.Context, userID string) (string, string, error)
	VerifyTwoFactor(ctx context.Context, userID, email, code, secret string) (string, *entities.User, bool, error)
	EnableTwoFactor(ctx context.Context, userID, code, secret string) error
	DisableTwoFactor(ctx context.Context, userID string) error
}

type loginAttempt struct {
	count     int
	lastTrial time.Time
}

type authUsecase struct {
	repo          repositories.UserRepository
	tenantRepo    tenantrepositories.TenantRepository
	tokenMaker    auth.TokenMaker
	loginAttempts sync.Map // email -> *loginAttempt
}

func NewAuthUsecase(repo repositories.UserRepository, tenantRepo tenantrepositories.TenantRepository, tokenMaker auth.TokenMaker) AuthUsecase {
	return &authUsecase{
		repo:          repo,
		tenantRepo:    tenantRepo,
		tokenMaker:    tokenMaker,
		loginAttempts: sync.Map{},
	}
}

func (u *authUsecase) SignIn(ctx context.Context, email, password string) (*entities.User, string, bool, bool, string, string, error) {
	// Rate limiting: trial login to 5
	val, _ := u.loginAttempts.Load(email)
	attempt, ok := val.(*loginAttempt)
	if !ok {
		attempt = &loginAttempt{count: 0}
		u.loginAttempts.Store(email, attempt)
	}

	if attempt.count >= 5 && time.Since(attempt.lastTrial) < 15*time.Minute {
		return nil, "", false, false, "", "", fmt.Errorf("too many login attempts. please try again in 15 minutes")
	}

	user, err := u.repo.GetByEmail(ctx, email)
	if err != nil {
		attempt.count++
		attempt.lastTrial = time.Now()
		return nil, "", false, false, "", "", errors.New("invalid email or password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		attempt.count++
		attempt.lastTrial = time.Now()
		return nil, "", false, false, "", "", errors.New("invalid email or password")
	}

	// Reset attempts on success
	u.loginAttempts.Delete(email)

	if user.TwoFactorEnabled {
		if user.TwoFactorSecret == "" {
			secret, qrCodeURL, err := u.SetupTwoFactor(ctx, user.ID)
			if err != nil {
				return user, "", false, true, "", "", err
			}
			return user, "", false, true, secret, qrCodeURL, nil
		}
		return user, "", true, false, "", "", nil
	}

	// Generate Paseto token. Duration could be configurable, but 24h is a sane default for production.
	token, err := u.tokenMaker.CreateToken(user.ID, user.TenantID, user.Role, 24*time.Hour)
	if err != nil {
		return nil, "", false, false, "", "", err
	}

	return user, token, false, false, "", "", nil
}

func (u *authUsecase) GetCurrentUser(ctx context.Context, userID string) (*entities.User, error) {
	return u.repo.GetByID(ctx, userID)
}

func (u *authUsecase) CreateAdmin(ctx context.Context, email, password, name string) error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// Create default tenant
	tenantID := uuid.New().String()
	tenant := &tenantentities.Tenant{
		ID:        tenantID,
		Name:      "Default Tenant",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := u.tenantRepo.Create(ctx, tenant); err != nil {
		return err
	}

	user := &entities.User{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Email:     email,
		Password:  string(hashedPassword),
		Name:      name,
		Role:      "USER_ROLE_SUPER_ADMIN",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return u.repo.Create(ctx, user)
}

func (u *authUsecase) IsFirstRun(ctx context.Context) (bool, error) {
	count, err := u.repo.Count(ctx)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

func (u *authUsecase) SetupTwoFactor(ctx context.Context, userID string) (string, string, error) {
	user, err := u.repo.GetByID(ctx, userID)
	if err != nil {
		return "", "", err
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Panmail",
		AccountName: user.Email,
	})
	if err != nil {
		return "", "", err
	}

	return key.Secret(), key.URL(), nil
}

func (u *authUsecase) VerifyTwoFactor(ctx context.Context, userID, email, code, secret string) (string, *entities.User, bool, error) {
	var user *entities.User
	var err error

	if userID != "" {
		user, err = u.repo.GetByID(ctx, userID)
	} else if email != "" {
		user, err = u.repo.GetByEmail(ctx, email)
	} else {
		return "", nil, false, errors.New("user identification required")
	}

	if err != nil {
		return "", nil, false, err
	}

	// If secret is provided, we are in setup mode
	verifySecret := user.TwoFactorSecret
	if secret != "" {
		verifySecret = secret
	}

	if verifySecret == "" {
		return "", nil, false, errors.New("two factor not set up")
	}

	valid := totp.Validate(code, verifySecret)
	if !valid {
		return "", nil, false, nil
	}

	// If secret was provided and valid, enable 2FA for this user
	if secret != "" && user != nil {
		if err := u.repo.UpdateTwoFactor(ctx, user.ID, true, secret); err != nil {
			return "", user, true, err
		}
	}

	token, err := u.tokenMaker.CreateToken(user.ID, user.TenantID, user.Role, 24*time.Hour)
	if err != nil {
		return "", user, true, err
	}

	return token, user, true, nil
}

func (u *authUsecase) EnableTwoFactor(ctx context.Context, userID, code, secret string) error {
	valid := totp.Validate(code, secret)
	if !valid {
		return errors.New("invalid verification code")
	}

	return u.repo.UpdateTwoFactor(ctx, userID, true, secret)
}

func (u *authUsecase) DisableTwoFactor(ctx context.Context, userID string) error {
	return u.repo.UpdateTwoFactor(ctx, userID, false, "")
}
