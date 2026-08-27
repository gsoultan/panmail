package stores

import (
	"context"

	"github.com/gsoultan/panmail/internal/suppression/repositories/entities"
)

type SuppressionRepository interface {
	Create(ctx context.Context, s *entities.Suppression) error
	Delete(ctx context.Context, tenantID, email string) error
	GetByEmail(ctx context.Context, tenantID, email string) (*entities.Suppression, error)

	// GetByEmails answers for many addresses in one round trip, keyed by the
	// lower-cased address. An address absent from the map is not suppressed.
	GetByEmails(ctx context.Context, tenantID string, emails []string) (map[string]*entities.Suppression, error)
	List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.Suppression, string, error)
}
