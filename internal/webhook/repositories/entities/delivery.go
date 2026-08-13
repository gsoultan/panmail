package entities

import "time"

// DeliveryStatus is where a webhook notification has got to.
type DeliveryStatus string

const (
	DeliveryStatusPending   DeliveryStatus = "PENDING"
	DeliveryStatusDeferred  DeliveryStatus = "DEFERRED"
	DeliveryStatusDelivered DeliveryStatus = "DELIVERED"
	DeliveryStatusFailed    DeliveryStatus = "FAILED"

	// DeliveryStatusSending marks a notification a worker has claimed. The
	// claim carries a deadline, so one left in this state by a crashed worker
	// becomes available again rather than being stranded.
	DeliveryStatusSending DeliveryStatus = "SENDING"
)

// WebhookDelivery is one notification owed to one tenant.
//
// Persisted rather than held in a channel because the previous design lost
// them: a failing endpoint, a full queue or a restart each dropped the
// notification with nothing left to replay. A tenant relying on these to learn
// that mail bounced simply did not learn it.
type WebhookDelivery struct {
	ID       string
	TenantID string
	// The subscription this is owed to. A reference rather than a copied URL,
	// so correcting a wrong endpoint redirects the retries still pending, and
	// deleting the subscription stops them.
	WebhookID string

	Event string
	// The payload as promised at the moment the event happened. Stored rather
	// than re-derived later from mutable records, which would deliver
	// something subtly different from what the event described.
	Payload []byte

	Status        DeliveryStatus
	AttemptCount  int
	NextAttemptAt time.Time
	LastError     string

	CreatedAt time.Time
	UpdatedAt time.Time
}
