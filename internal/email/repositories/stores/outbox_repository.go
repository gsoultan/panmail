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
}
