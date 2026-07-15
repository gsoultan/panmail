package stores

import (
	"context"

	"github.com/gsoultan/panmail/internal/template/repositories/entities"
)

type TemplateRepository interface {
	Create(ctx context.Context, template *entities.Template) error
	GetByID(ctx context.Context, tenantID, id string) (*entities.Template, error)
	List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.Template, string, error)
	Update(ctx context.Context, template *entities.Template) error
	Delete(ctx context.Context, tenantID, id string) error
}
