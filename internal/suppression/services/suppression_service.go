package services

import (
	"context"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/suppression/usecases"
)

type suppressionService struct {
	manageSuppressionsUsecase usecases.ManageSuppressionsUsecase
}

func NewSuppressionService(manageSuppressionsUsecase usecases.ManageSuppressionsUsecase) panmailv1connect.SuppressionServiceHandler {
	return &suppressionService{
		manageSuppressionsUsecase: manageSuppressionsUsecase,
	}
}

func (s *suppressionService) AddSuppression(ctx context.Context, req *connect.Request[panmailv1.AddSuppressionRequest]) (*connect.Response[panmailv1.AddSuppressionResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	sup, err := s.manageSuppressionsUsecase.Add(ctx, tenantID, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.AddSuppressionResponse{Suppression: sup}), nil
}

func (s *suppressionService) RemoveSuppression(ctx context.Context, req *connect.Request[panmailv1.RemoveSuppressionRequest]) (*connect.Response[panmailv1.RemoveSuppressionResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	if err := s.manageSuppressionsUsecase.Remove(ctx, tenantID, req.Msg.Email); err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.RemoveSuppressionResponse{}), nil
}

func (s *suppressionService) ListSuppressions(ctx context.Context, req *connect.Request[panmailv1.ListSuppressionsRequest]) (*connect.Response[panmailv1.ListSuppressionsResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	sups, nextToken, err := s.manageSuppressionsUsecase.List(ctx, tenantID, int(req.Msg.PageSize), req.Msg.PageToken)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.ListSuppressionsResponse{
		Suppressions:  sups,
		NextPageToken: nextToken,
	}), nil
}

func (s *suppressionService) CheckSuppression(ctx context.Context, req *connect.Request[panmailv1.CheckSuppressionRequest]) (*connect.Response[panmailv1.CheckSuppressionResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	isSuppressed, reason, err := s.manageSuppressionsUsecase.Check(ctx, tenantID, req.Msg.Email)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.CheckSuppressionResponse{
		IsSuppressed: isSuppressed,
		Reason:       reason,
	}), nil
}
