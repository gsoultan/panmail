package entities

import "time"

// UserTenant is one user's membership of one tenant.
//
// A user's home tenant (users.tenant_id) also has a row here, so the set of
// tenants a user can reach is answerable from this table alone. Home
// membership is not removable — see MembershipUsecase.RemoveUserFromTenant.
type UserTenant struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`

	// TenantName is filled in by reads that join tenants, so a caller can
	// render a membership without a second round trip. Writes ignore it.
	TenantName string `json:"tenant_name,omitempty"`

	// Role is what the user may do inside this tenant, and nothing else. It is
	// never USER_ROLE_SUPER_ADMIN: that role reaches tenant-wide procedures
	// and so is held globally on the user, not per membership.
	Role string `json:"role"`

	// IsHome marks the membership that matches users.tenant_id.
	IsHome bool `json:"is_home"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
