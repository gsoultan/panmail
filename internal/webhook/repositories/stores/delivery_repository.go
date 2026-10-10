package stores

import (
	"context"
	"errors"
	"time"

	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
)

// ErrClaimLost is returned by Update when the row no longer carries the claim
// the caller read it under: the lease ran out and another worker has it, or it
// was never claimed. Nothing is written.
var ErrClaimLost = errors.New("webhook delivery is no longer claimed by this worker")

// DeliveryRepository stores webhook notifications until they are delivered or
// give up.
type DeliveryRepository interface {
	Create(ctx context.Context, d *entities.WebhookDelivery) error

	// ClaimDue atomically takes ownership of up to limit due notifications for
	// leaseFor, and returns them.
	//
	// Two processes polling the same table must never both receive the same
	// notification, or the tenant's endpoint is called twice for one event —
	// and a consumer that creates a ticket per webhook creates two.
	//
	// No more than perSubscription of them belong to any one subscription, and
	// the batch is filled round-robin across subscriptions, oldest first within
	// each. One subscription with a deep backlog cannot take a whole batch while
	// another has notifications waiting.
	ClaimDue(ctx context.Context, limit, perSubscription int, leaseFor time.Duration) ([]*entities.WebhookDelivery, error)

	// DeferSubscription moves every unclaimed, waiting notification for one
	// subscription to no earlier than until. Used when an endpoint has stopped
	// answering, so the next claim does not hand it more workers to hold.
	// Claimed rows are left to the worker holding them.
	DeferSubscription(ctx context.Context, webhookID string, until time.Time) error

	// Update records an outcome for a notification the caller claimed, and
	// releases the claim. It applies only while the row still carries
	// d.ClaimToken, and returns ErrClaimLost otherwise.
	Update(ctx context.Context, d *entities.WebhookDelivery) error
	Delete(ctx context.Context, id string) error

	// PruneTerminal removes notifications that have finished, older than the
	// cutoff, so the table does not grow for the life of the deployment.
	PruneTerminal(ctx context.Context, olderThan time.Time) (int64, error)

	// Stats reports how much is queued and how old the oldest of it is. The
	// age is what distinguishes a busy queue from one that has stopped.
	Stats(ctx context.Context) (pending int64, oldest time.Time, err error)
}
