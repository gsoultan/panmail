package connect

import (
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"net/http"
)

func NewHandler(service panmailv1connect.WebhookServiceHandler) (string, http.Handler) {
	return panmailv1connect.NewWebhookServiceHandler(service)
}
