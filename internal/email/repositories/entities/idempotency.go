package entities

import "time"

// IdempotencyKey records that a send carrying a client's key was accepted, so
// a repeat of it can be answered with the same message instead of queueing
// another.
type IdempotencyKey struct {
	TenantID string
	Key      string

	// RequestHash identifies the request the key was first used with. A repeat
	// is only a repeat if it asks for the same thing.
	RequestHash string

	MessageID string
	CreatedAt time.Time
}
