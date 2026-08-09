package stores

import (
	"context"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
)

type OutboxRepository interface {
	Create(ctx context.Context, email *entities.OutboxEmail) error
	GetByID(ctx context.Context, id string) (*entities.OutboxEmail, error)

	// ClaimPending atomically takes ownership of up to limit due messages for
	// leaseFor, and returns them. Two processes polling the same table must
	// never both receive the same message, or the recipient gets it twice.
	//
	// A lease that expires without the message being resolved is reclaimable,
	// so a worker that dies mid-send does not strand mail.
	ClaimPending(ctx context.Context, limit int, leaseFor time.Duration) ([]*entities.OutboxEmail, error)

	Update(ctx context.Context, email *entities.OutboxEmail) error
	Delete(ctx context.Context, id string) error
	CountPending(ctx context.Context, tenantID string) (int64, error)

	// PruneTerminal removes messages in a terminal state last touched before
	// the cutoff, and reports how many went.
	//
	// Only failures accumulate — a delivered message is deleted outright — but
	// they accumulate forever, and each row carries the whole serialised
	// request including the body. Over the life of a deployment that is the
	// largest table in the database, holding nothing anyone will read.
	PruneTerminal(ctx context.Context, olderThan time.Time) (int64, error)
}
