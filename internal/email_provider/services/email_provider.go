package services

import (
	"context"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type EmailProviderService interface {
	CreateEmailProvider(ctx context.Context, req *panmailv1.CreateEmailProviderRequest) (*panmailv1.CreateEmailProviderResponse, error)
	GetEmailProvider(ctx context.Context, req *panmailv1.GetEmailProviderRequest) (*panmailv1.GetEmailProviderResponse, error)
	ListEmailProviders(ctx context.Context, req *panmailv1.ListEmailProvidersRequest) (*panmailv1.ListEmailProvidersResponse, error)
	UpdateEmailProvider(ctx context.Context, req *panmailv1.UpdateEmailProviderRequest) (*panmailv1.UpdateEmailProviderResponse, error)
	DeleteEmailProvider(ctx context.Context, req *panmailv1.DeleteEmailProviderRequest) (*panmailv1.DeleteEmailProviderResponse, error)
	TestEmailProvider(ctx context.Context, req *panmailv1.TestEmailProviderRequest) (*panmailv1.TestEmailProviderResponse, error)
	TestEmailProviderConfig(ctx context.Context, req *panmailv1.CreateEmailProviderRequest) (*panmailv1.TestEmailProviderResponse, error)
}
