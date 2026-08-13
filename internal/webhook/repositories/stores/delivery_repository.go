package stores

import (
	"context"
	"time"

	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
)

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
	ClaimDue(ctx context.Context, limit int, leaseFor time.Duration) ([]*entities.WebhookDelivery, error)

	Update(ctx context.Context, d *entities.WebhookDelivery) error
	Delete(ctx context.Context, id string) error

	// PruneTerminal removes notifications that have finished, older than the
	// cutoff, so the table does not grow for the life of the deployment.
	PruneTerminal(ctx context.Context, olderThan time.Time) (int64, error)
}
