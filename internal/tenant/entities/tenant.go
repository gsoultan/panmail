package entities

import "time"

type Tenant struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RetryPattern []string  `json:"retry_pattern"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
