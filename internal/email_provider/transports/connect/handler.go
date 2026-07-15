package connect

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/email_provider/services"
)

type handler struct {
	service services.EmailProviderService
}

func NewHandler(service services.EmailProviderService, opts ...connect.HandlerOption) (string, http.Handler) {
	h := &handler{service: service}
	return panmailv1connect.NewEmailProviderServiceHandler(h, opts...)
}

func (h *handler) CreateEmailProvider(ctx context.Context, req *connect.Request[panmailv1.CreateEmailProviderRequest]) (*connect.Response[panmailv1.CreateEmailProviderResponse], error) {
	res, err := h.service.CreateEmailProvider(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *handler) GetEmailProvider(ctx context.Context, req *connect.Request[panmailv1.GetEmailProviderRequest]) (*connect.Response[panmailv1.GetEmailProviderResponse], error) {
	res, err := h.service.GetEmailProvider(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *handler) ListEmailProviders(ctx context.Context, req *connect.Request[panmailv1.ListEmailProvidersRequest]) (*connect.Response[panmailv1.ListEmailProvidersResponse], error) {
	res, err := h.service.ListEmailProviders(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *handler) UpdateEmailProvider(ctx context.Context, req *connect.Request[panmailv1.UpdateEmailProviderRequest]) (*connect.Response[panmailv1.UpdateEmailProviderResponse], error) {
	res, err := h.service.UpdateEmailProvider(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *handler) DeleteEmailProvider(ctx context.Context, req *connect.Request[panmailv1.DeleteEmailProviderRequest]) (*connect.Response[panmailv1.DeleteEmailProviderResponse], error) {
	res, err := h.service.DeleteEmailProvider(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *handler) TestEmailProvider(ctx context.Context, req *connect.Request[panmailv1.TestEmailProviderRequest]) (*connect.Response[panmailv1.TestEmailProviderResponse], error) {
	res, err := h.service.TestEmailProvider(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *handler) TestEmailProviderConfig(ctx context.Context, req *connect.Request[panmailv1.CreateEmailProviderRequest]) (*connect.Response[panmailv1.TestEmailProviderResponse], error) {
	res, err := h.service.TestEmailProviderConfig(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}
