package services

import (
	"context"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/inbound/usecases"
)

type inboundService struct {
	usecase usecases.InboundUsecase
}

func NewInboundService(usecase usecases.InboundUsecase) panmailv1connect.InboundServiceHandler {
	return &inboundService{usecase: usecase}
}

func (s *inboundService) ListInboundEmails(ctx context.Context, req *connect.Request[panmailv1.ListInboundEmailsRequest]) (*connect.Response[panmailv1.ListInboundEmailsResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	emails, nextPageToken, err := s.usecase.List(ctx, tenantID, int(req.Msg.PageSize), req.Msg.PageToken)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.ListInboundEmailsResponse{
		Emails:        emails,
		NextPageToken: nextPageToken,
	}), nil
}

func (s *inboundService) GetInboundEmail(ctx context.Context, req *connect.Request[panmailv1.GetInboundEmailRequest]) (*connect.Response[panmailv1.GetInboundEmailResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	email, err := s.usecase.Get(ctx, tenantID, req.Msg.Id)
	if err != nil {
		return nil, err
	}
	if email == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return connect.NewResponse(&panmailv1.GetInboundEmailResponse{
		Email: email,
	}), nil
}
