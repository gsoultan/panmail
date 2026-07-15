package services

import (
	"context"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	authmiddlewares "github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/webhook/usecases"
)

type webhookService struct {
	usecase usecases.WebhookUsecase
}

func NewWebhookService(usecase usecases.WebhookUsecase) panmailv1connect.WebhookServiceHandler {
	return &webhookService{usecase: usecase}
}

func (s *webhookService) CreateWebhook(ctx context.Context, req *connect.Request[panmailv1.CreateWebhookRequest]) (*connect.Response[panmailv1.CreateWebhookResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	webhook, err := s.usecase.Create(ctx, tenantID, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.CreateWebhookResponse{Webhook: webhook}), nil
}

func (s *webhookService) ListWebhooks(ctx context.Context, req *connect.Request[panmailv1.ListWebhooksRequest]) (*connect.Response[panmailv1.ListWebhooksResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	webhooks, nextPageToken, err := s.usecase.List(ctx, tenantID, int(req.Msg.PageSize), req.Msg.PageToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.ListWebhooksResponse{
		Webhooks:      webhooks,
		NextPageToken: nextPageToken,
	}), nil
}

func (s *webhookService) DeleteWebhook(ctx context.Context, req *connect.Request[panmailv1.DeleteWebhookRequest]) (*connect.Response[panmailv1.DeleteWebhookResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	if err := s.usecase.Delete(ctx, tenantID, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.DeleteWebhookResponse{}), nil
}

func (s *webhookService) UpdateWebhook(ctx context.Context, req *connect.Request[panmailv1.UpdateWebhookRequest]) (*connect.Response[panmailv1.UpdateWebhookResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	webhook, err := s.usecase.Update(ctx, tenantID, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.UpdateWebhookResponse{Webhook: webhook}), nil
}
