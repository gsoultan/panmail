package middlewares

import (
	"context"
	"slices"

	"github.com/gsoultan/panmail/internal/auth/entities"
)

type ContextKey string

const (
	UserIDKey    ContextKey = "user_id"
	TenantIDKey  ContextKey = "tenant_id"
	RoleKey      ContextKey = "role"
	PrincipalKey ContextKey = "principal"
)

// PrincipalKind distinguishes how a caller authenticated, because the two
// carry different authority: a user has a role, an API key has scopes.
type PrincipalKind string

const (
	PrincipalUser   PrincipalKind = "user"
	PrincipalAPIKey PrincipalKind = "api_key"
)

// Principal is the authenticated caller. Its absence from a context means the
// request is unauthenticated.
type Principal struct {
	Kind     PrincipalKind
	UserID   string
	TenantID string
	Role     string
	Scopes   []entities.Scope
}

// HasScope reports whether an API-key principal holds the given capability.
func (p *Principal) HasScope(scope entities.Scope) bool {
	return slices.Contains(p.Scopes, scope)
}

// WithPrincipal returns a context carrying the authenticated caller.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	ctx = context.WithValue(ctx, PrincipalKey, p)
	ctx = context.WithValue(ctx, TenantIDKey, p.TenantID)
	ctx = context.WithValue(ctx, RoleKey, p.Role)
	if p.UserID != "" {
		ctx = context.WithValue(ctx, UserIDKey, p.UserID)
	}
	return ctx
}

// GetPrincipal returns the authenticated caller, if any.
func GetPrincipal(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(PrincipalKey).(*Principal)
	return p, ok && p != nil
}

// GetUserID returns the signed-in user's id. API-key callers have none.
func GetUserID(ctx context.Context) (string, bool) {
	val, ok := ctx.Value(UserIDKey).(string)
	return val, ok && val != ""
}

func GetTenantID(ctx context.Context) string {
	if val, ok := ctx.Value(TenantIDKey).(string); ok {
		return val
	}
	return ""
}

func GetRole(ctx context.Context) string {
	if role, ok := ctx.Value(RoleKey).(string); ok {
		return role
	}
	return ""
}

// HasRole reports whether the caller meets any of the given roles.
func HasRole(ctx context.Context, requiredRoles ...string) bool {
	role := GetRole(ctx)
	for _, required := range requiredRoles {
		if roleSatisfies(role, required) {
			return true
		}
	}
	return false
}
