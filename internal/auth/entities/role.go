package entities

// Role names as they appear in tokens, in users.role, and in
// user_tenants.role. They live here rather than beside the RBAC policy because
// both the policy (a middleware) and the usecases that assign roles need them,
// and a usecase must not import a middleware.
const (
	RoleViewer     = "USER_ROLE_VIEWER"
	RoleEditor     = "USER_ROLE_EDITOR"
	RoleAdmin      = "USER_ROLE_ADMIN"
	RoleSuperAdmin = "USER_ROLE_SUPER_ADMIN"
)

// IsTenantRole reports whether a role may be held as a membership of one
// tenant.
//
// USER_ROLE_SUPER_ADMIN is deliberately excluded. It reaches procedures that
// are not scoped to any tenant — ListTenants, CreateTenant — so granting it as
// part of "add this user to that tenant" would hand out global authority under
// the guise of a local one. Super admin is set on the user, by UpdateUserRole.
func IsTenantRole(role string) bool {
	switch role {
	case RoleViewer, RoleEditor, RoleAdmin:
		return true
	default:
		return false
	}
}
