package entities

import "time"

type User struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	Email            string    `json:"email"`
	Password         string    `json:"-"`
	Name             string    `json:"name"`
	Role             string    `json:"role"`
	TwoFactorEnabled bool      `json:"two_factor_enabled"`
	TwoFactorSecret  string    `json:"-"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
