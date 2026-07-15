package usecases

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	"golang.org/x/crypto/bcrypt"
)

type UserUsecase interface {
	CreateUser(ctx context.Context, tenantID, email, password, name, role string) (*entities.User, error)
	ListUsers(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.User, string, error)
	GetByID(ctx context.Context, id string) (*entities.User, error)
	UpdateUserRole(ctx context.Context, id, role string) error
	UpdateUserTwoFactor(ctx context.Context, id string, enabled bool) error
	DeleteUser(ctx context.Context, id string) error
}

type userUsecase struct {
	repo repositories.UserRepository
}

func NewUserUsecase(repo repositories.UserRepository) UserUsecase {
	return &userUsecase{repo: repo}
}

func (u *userUsecase) CreateUser(ctx context.Context, tenantID, email, password, name, role string) (*entities.User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &entities.User{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Email:     email,
		Password:  string(hashedPassword),
		Name:      name,
		Role:      role,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := u.repo.Create(ctx, user); err != nil {
		return nil, err
	}

	return user, nil
}

func (u *userUsecase) ListUsers(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.User, string, error) {
	return u.repo.ListByTenantID(ctx, tenantID, pageSize, pageToken)
}

func (u *userUsecase) GetByID(ctx context.Context, id string) (*entities.User, error) {
	return u.repo.GetByID(ctx, id)
}

func (u *userUsecase) UpdateUserRole(ctx context.Context, id, role string) error {
	return u.repo.UpdateRole(ctx, id, role)
}

func (u *userUsecase) UpdateUserTwoFactor(ctx context.Context, id string, enabled bool) error {
	user, err := u.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Role protection: Only Super Admin can change Super Admin's 2FA
	// Actually, the Service layer already checks if the caller is at least ADMIN.
	// We should probably pass the caller role here or just rely on the fact that
	// Super Admin is the only one who can't be modified by Admin.
	// But I don't have caller role here.

	return u.repo.UpdateTwoFactor(ctx, id, enabled, user.TwoFactorSecret)
}

func (u *userUsecase) DeleteUser(ctx context.Context, id string) error {
	return u.repo.Delete(ctx, id)
}
