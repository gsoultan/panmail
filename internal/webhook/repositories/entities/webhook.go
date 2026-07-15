package entities

import (
	"time"
)

type Webhook struct {
	ID        string    `db:"id"`
	TenantID  string    `db:"tenant_id"`
	Name      string    `db:"name"`
	URL       string    `db:"url"`
	Events    []int32   `db:"events"` // Store as JSON in DB
	Active    bool      `db:"active"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}
