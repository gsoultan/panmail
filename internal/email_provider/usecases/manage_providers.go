package usecases

import (
	"context"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type ManageProvidersUsecase interface {
	Create(ctx context.Context, tenantID string, req *panmailv1.CreateEmailProviderRequest) (*panmailv1.EmailProvider, error)
	Get(ctx context.Context, tenantID, id string) (*panmailv1.EmailProvider, error)
	List(ctx context.Context, tenantID string, name string, providerType panmailv1.ProviderType, pageSize int, pageToken string) ([]*panmailv1.EmailProvider, string, error)
	Update(ctx context.Context, tenantID string, req *panmailv1.UpdateEmailProviderRequest) (*panmailv1.EmailProvider, error)
	Delete(ctx context.Context, tenantID, id string) error
	Test(ctx context.Context, tenantID, id string) error
	TestConfig(ctx context.Context, req *panmailv1.CreateEmailProviderRequest) error
}
