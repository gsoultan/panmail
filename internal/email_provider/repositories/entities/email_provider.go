package entities

import (
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type EmailProvider struct {
	ID             string
	TenantID       string
	Name           string
	Type           panmailv1.ProviderType
	Config         []byte   // JSON encoded config
	AllowedDomains []string // Domains this provider is authorized to send for
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ProviderFactory interface {
	CreateSender(p *EmailProvider) (any, error)
	CreateReceiver(p *EmailProvider) (any, error)
}
