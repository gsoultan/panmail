package usecases

import (
	"context"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type SendEmailUsecase interface {
	SendEmail(ctx context.Context, tenantID string, req *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error)
}
