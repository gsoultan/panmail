package services

import (
	"context"
	"time"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/tenant/usecases"
)

type tenantService struct {
	panmailv1connect.UnimplementedTenantServiceHandler
	usecase usecases.TenantUsecase
}

func NewTenantService(usecase usecases.TenantUsecase) panmailv1connect.TenantServiceHandler {
	return &tenantService{usecase: usecase}
}

func (s *tenantService) CreateTenant(ctx context.Context, req *connect.Request[panmailv1.CreateTenantRequest]) (*connect.Response[panmailv1.CreateTenantResponse], error) {
	tenant, err := s.usecase.CreateTenant(ctx, req.Msg.Name, req.Msg.RetryPattern)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.CreateTenantResponse{
		Tenant: &panmailv1.Tenant{
			Id:           tenant.ID,
			Name:         tenant.Name,
			RetryPattern: tenant.RetryPattern,
			CreatedAt:    tenant.CreatedAt.Format(time.RFC3339),
		},
	}), nil
}

func (s *tenantService) ListTenants(ctx context.Context, req *connect.Request[panmailv1.ListTenantsRequest]) (*connect.Response[panmailv1.ListTenantsResponse], error) {
	tenants, nextPageToken, err := s.usecase.ListTenants(ctx, int(req.Msg.PageSize), req.Msg.PageToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var protoTenants []*panmailv1.Tenant
	for _, t := range tenants {
		protoTenants = append(protoTenants, &panmailv1.Tenant{
			Id:           t.ID,
			Name:         t.Name,
			RetryPattern: t.RetryPattern,
			CreatedAt:    t.CreatedAt.Format(time.RFC3339),
		})
	}

	return connect.NewResponse(&panmailv1.ListTenantsResponse{
		Tenants:       protoTenants,
		NextPageToken: nextPageToken,
	}), nil
}

func (s *tenantService) UpdateTenant(ctx context.Context, req *connect.Request[panmailv1.UpdateTenantRequest]) (*connect.Response[panmailv1.UpdateTenantResponse], error) {
	tenant, err := s.usecase.UpdateTenant(ctx, req.Msg.Id, req.Msg.Name, req.Msg.RetryPattern)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.UpdateTenantResponse{
		Tenant: &panmailv1.Tenant{
			Id:           tenant.ID,
			Name:         tenant.Name,
			RetryPattern: tenant.RetryPattern,
			CreatedAt:    tenant.CreatedAt.Format(time.RFC3339),
		},
	}), nil
}

func (s *tenantService) DeleteTenant(ctx context.Context, req *connect.Request[panmailv1.DeleteTenantRequest]) (*connect.Response[panmailv1.DeleteTenantResponse], error) {
	if err := s.usecase.DeleteTenant(ctx, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.DeleteTenantResponse{}), nil
}
