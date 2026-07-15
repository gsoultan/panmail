package usecases

import (
	"context"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type InboundUsecase interface {
	Process(ctx context.Context, email *panmailv1.InboundEmail) error
	List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.InboundEmail, string, error)
	Get(ctx context.Context, tenantID, id string) (*panmailv1.InboundEmail, error)
}

type WebhookTrigger interface {
	Enqueue(tenantID string, event panmailv1.WebhookTriggerEvent, payload any)
}
