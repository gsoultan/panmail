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

	// OutboxStatusHeld marks a message a filter rule quarantined. The claim
	// query takes PENDING, DEFERRED and expired SENDING rows, so a held one is
	// invisible to the worker without that query needing to know this status
	// exists — which is the reason the outbox is where a held message waits
	// rather than a store of its own. The bytes are already here, already
	// durable, already scoped to the tenant, and releasing is a status flip
	// rather than a second serialisation of the same request.
	OutboxStatusHeld OutboxStatus = "HELD"
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
