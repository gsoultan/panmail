package entities

import (
	"time"
)

type OutboxStatus string

const (
	OutboxStatusPending  OutboxStatus = "PENDING"
	OutboxStatusDeferred OutboxStatus = "DEFERRED"
	OutboxStatusFailed   OutboxStatus = "FAILED"
)

type OutboxEmail struct {
	ID          string
	TenantID    string
	Request     []byte // JSON encoded panmailv1.SendEmailRequest
	Status      OutboxStatus
	RetryCount  int
	NextRetryAt time.Time
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
