package entities

import (
	"time"
)

type Webhook struct {
	ID       string  `db:"id"`
	TenantID string  `db:"tenant_id"`
	Name     string  `db:"name"`
	URL      string  `db:"url"`
	Events   []int32 `db:"events"` // Store as JSON in DB
	Active   bool    `db:"active"`

	// Secret signs the notifications sent to URL, so the receiver can tell a
	// real one from a forged one. Encrypted at rest and never returned by the
	// API; empty means this subscription predates signing.
	Secret string `db:"secret"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}
