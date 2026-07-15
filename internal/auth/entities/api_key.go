package entities

import (
	"time"
)

type ApiKey struct {
	ID         string
	TenantID   string
	Name       string
	KeyHash    string
	Prefix     string
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	IsEnabled  bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
