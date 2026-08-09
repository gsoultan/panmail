package entities

import (
	"time"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type EmailProvider struct {
	ID             string
	TenantID       string
	Name           string
	Type           panmailv1.ProviderType
	Config         []byte   // JSON encoded config; encrypted at rest
	AllowedDomains []string // Domains this provider is authorized to send for

	// WebhookSecret verifies delivery webhooks from this provider: SendGrid's
	// ECDSA public key, Mailgun's signing key, or a shared HMAC secret.
	// Encrypted at rest and never returned by the API.
	WebhookSecret string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProviderFactory builds transports for a configured provider.
//
// It returns the concrete gsmail interfaces rather than `any`: callers used to
// type-assert the result and quietly skip the provider when the assertion
// failed, turning a configuration error into a silently undelivered message.
type ProviderFactory interface {
	CreateSender(p *EmailProvider) (gsmail.Sender, error)
	CreateReceiver(p *EmailProvider) (gsmail.Receiver, error)
	Close() error
}
