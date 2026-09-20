package services

import (
	"context"
	"errors"
	"fmt"

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
		if errors.Is(err, usecases.ErrEmptyAddress) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, err
	}
	return connect.NewResponse(&panmailv1.AddSuppressionResponse{Suppression: sup}), nil
}

// ImportSuppressions adds a list from another provider in one pass.
//
// An oversized request is InvalidArgument rather than a truncated import: half
// a suppression list written with no indication of which half is worse than a
// refusal the caller can act on by splitting the file.
func (s *suppressionService) ImportSuppressions(ctx context.Context, req *connect.Request[panmailv1.ImportSuppressionsRequest]) (*connect.Response[panmailv1.ImportSuppressionsResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)

	if len(req.Msg.Entries) > usecases.MaxImportEntries {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"too many entries: %d, limit is %d per request",
			len(req.Msg.Entries), usecases.MaxImportEntries))
	}

	result, err := s.manageSuppressionsUsecase.Import(ctx, tenantID, req.Msg.Entries)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.ImportSuppressionsResponse{
		Imported:          int32(result.Imported),
		AlreadySuppressed: int32(result.AlreadySuppressed),
		Invalid:           int32(result.Invalid),
		InvalidSamples:    result.InvalidSamples,
	}), nil
}

func (s *suppressionService) RemoveSuppression(ctx context.Context, req *connect.Request[panmailv1.RemoveSuppressionRequest]) (*connect.Response[panmailv1.RemoveSuppressionResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	if err := s.manageSuppressionsUsecase.Remove(ctx, tenantID, req.Msg.Email); err != nil {
		if errors.Is(err, usecases.ErrEmptyAddress) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
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
