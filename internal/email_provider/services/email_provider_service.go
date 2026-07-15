package services

import (
	"context"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/email_provider/usecases"
)

type emailProviderService struct {
	manageProvidersUsecase usecases.ManageProvidersUsecase
}

func NewEmailProviderService(manageProvidersUsecase usecases.ManageProvidersUsecase) EmailProviderService {
	return &emailProviderService{
		manageProvidersUsecase: manageProvidersUsecase,
	}
}

func (s *emailProviderService) CreateEmailProvider(ctx context.Context, req *panmailv1.CreateEmailProviderRequest) (*panmailv1.CreateEmailProviderResponse, error) {
	tenantID := middlewares.GetTenantID(ctx)
	p, err := s.manageProvidersUsecase.Create(ctx, tenantID, req)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &panmailv1.CreateEmailProviderResponse{Provider: p}, nil
}

func (s *emailProviderService) GetEmailProvider(ctx context.Context, req *panmailv1.GetEmailProviderRequest) (*panmailv1.GetEmailProviderResponse, error) {
	tenantID := middlewares.GetTenantID(ctx)
	p, err := s.manageProvidersUsecase.Get(ctx, tenantID, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &panmailv1.GetEmailProviderResponse{Provider: p}, nil
}

func (s *emailProviderService) ListEmailProviders(ctx context.Context, req *panmailv1.ListEmailProvidersRequest) (*panmailv1.ListEmailProvidersResponse, error) {
	tenantID := middlewares.GetTenantID(ctx)
	providers, nextPageToken, err := s.manageProvidersUsecase.List(ctx, tenantID, int(req.PageSize), req.PageToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &panmailv1.ListEmailProvidersResponse{
		Providers:     providers,
		NextPageToken: nextPageToken,
	}, nil
}

func (s *emailProviderService) UpdateEmailProvider(ctx context.Context, req *panmailv1.UpdateEmailProviderRequest) (*panmailv1.UpdateEmailProviderResponse, error) {
	tenantID := middlewares.GetTenantID(ctx)
	p, err := s.manageProvidersUsecase.Update(ctx, tenantID, req)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &panmailv1.UpdateEmailProviderResponse{Provider: p}, nil
}

func (s *emailProviderService) DeleteEmailProvider(ctx context.Context, req *panmailv1.DeleteEmailProviderRequest) (*panmailv1.DeleteEmailProviderResponse, error) {
	tenantID := middlewares.GetTenantID(ctx)
	if err := s.manageProvidersUsecase.Delete(ctx, tenantID, req.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &panmailv1.DeleteEmailProviderResponse{}, nil
}

func (s *emailProviderService) TestEmailProvider(ctx context.Context, req *panmailv1.TestEmailProviderRequest) (*panmailv1.TestEmailProviderResponse, error) {
	tenantID := middlewares.GetTenantID(ctx)
	err := s.manageProvidersUsecase.Test(ctx, tenantID, req.Id)
	if err != nil {
		return &panmailv1.TestEmailProviderResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}
	return &panmailv1.TestEmailProviderResponse{Success: true}, nil
}

func (s *emailProviderService) TestEmailProviderConfig(ctx context.Context, req *panmailv1.CreateEmailProviderRequest) (*panmailv1.TestEmailProviderResponse, error) {
	err := s.manageProvidersUsecase.TestConfig(ctx, req)
	if err != nil {
		return &panmailv1.TestEmailProviderResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}
	return &panmailv1.TestEmailProviderResponse{Success: true}, nil
}
