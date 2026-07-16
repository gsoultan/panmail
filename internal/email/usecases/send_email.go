package usecases

import (
	"context"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type SendEmailUsecase interface {
	SendEmail(ctx context.Context, tenantID string, req *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error)
	RecordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient string, errorMessage string, metadata map[string]any) error
	RegisterQueueWorker(w QueueWorker)
}
