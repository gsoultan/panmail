package entities

import (
	"time"
)

type OutboxStatus string

const (
	OutboxStatusPending  OutboxStatus = "PENDING"
	OutboxStatusDeferred OutboxStatus = "DEFERRED"
	OutboxStatusFailed   OutboxStatus = "FAILED"

	// OutboxStatusSending marks a message a worker has claimed. The claim
	// carries a deadline, so a message left in this state by a crashed worker
	// becomes available again rather than being stranded.
	OutboxStatusSending OutboxStatus = "SENDING"
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
