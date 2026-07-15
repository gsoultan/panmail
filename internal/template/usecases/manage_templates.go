package usecases

import (
	"context"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type ManageTemplatesUsecase interface {
	Create(ctx context.Context, tenantID string, req *panmailv1.CreateTemplateRequest) (*panmailv1.Template, error)
	Get(ctx context.Context, tenantID, id string) (*panmailv1.Template, error)
	List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.Template, string, error)
	Update(ctx context.Context, tenantID string, req *panmailv1.UpdateTemplateRequest) (*panmailv1.Template, error)
	Delete(ctx context.Context, tenantID, id string) error
}
