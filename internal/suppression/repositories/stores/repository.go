package stores

import (
	"context"

	"github.com/gsoultan/panmail/internal/suppression/repositories/entities"
)

type SuppressionRepository interface {
	Create(ctx context.Context, s *entities.Suppression) error
	Delete(ctx context.Context, tenantID, email string) error
	GetByEmail(ctx context.Context, tenantID, email string) (*entities.Suppression, error)
	List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.Suppression, string, error)
}
