package stores

import (
	"context"

	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
)

type WebhookRepository interface {
	Create(ctx context.Context, webhook *entities.Webhook) error
	List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.Webhook, string, error)
	GetByID(ctx context.Context, tenantID, id string) (*entities.Webhook, error)
	Update(ctx context.Context, webhook *entities.Webhook) error
	Delete(ctx context.Context, tenantID, id string) error
	ListActiveByEvent(ctx context.Context, tenantID string, event int32) ([]*entities.Webhook, error)
}
