// Package smtp exposes the send pipeline over SMTP submission, so an
// application that already speaks SMTP can use panmail without adopting its
// RPC client.
//
// It is a transport and nothing more: every message it accepts goes through
// the same send usecase the ConnectRPC handler calls, and so through the same
// rate limit, backlog ceiling, suppression list and anti-spoofing checks.
package smtp

import (
	"context"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/entities"
)

// APIKeyVerifier resolves a submitted key to the tenant that owns it.
// usecases.ApiKeyUsecase satisfies it as written.
type APIKeyVerifier interface {
	VerifyApiKey(ctx context.Context, key string) (*entities.ApiKey, error)
}

// Sender submits a parsed message to the send pipeline.
// usecases.SendEmailUsecase satisfies it as written.
//
// The transport deliberately depends on the usecase rather than the service:
// the service's job is to translate outcomes into Connect codes, and SMTP
// needs those same outcomes translated into reply codes instead.
type Sender interface {
	SendEmail(ctx context.Context, tenantID string, req *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error)
}
