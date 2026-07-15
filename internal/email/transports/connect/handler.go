package connect

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
)

type handler struct {
	service panmailv1connect.EmailServiceHandler
}

func NewHandler(service panmailv1connect.EmailServiceHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	h := &handler{service: service}
	return panmailv1connect.NewEmailServiceHandler(h, opts...)
}

func (h *handler) SendEmail(ctx context.Context, req *connect.Request[panmailv1.SendEmailRequest]) (*connect.Response[panmailv1.SendEmailResponse], error) {
	return h.service.SendEmail(ctx, req)
}
