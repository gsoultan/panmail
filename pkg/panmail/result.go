package panmail

import panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"

// Status is the state a message is in. It is an alias for the API's own enum
// rather than a parallel set of constants, so the two cannot drift.
type Status = panmailv1.EmailEventType

const (
	// StatusPending is what a successful Send reports: the gateway has the
	// message on disk and will deliver it. Delivery itself is reported later,
	// through events and webhooks.
	StatusPending = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING

	StatusSent      = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT
	StatusDelivered = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED
	StatusDropped   = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DROPPED
)

// Result is what the gateway accepted.
type Result struct {
	// MessageID identifies the message for the rest of its life: delivery
	// events, webhook notifications and the analytics pages are all keyed by
	// it. Worth storing next to whatever prompted the send.
	MessageID string

	// Status at the moment of acceptance, which for a queued message is
	// StatusPending.
	Status Status
}
