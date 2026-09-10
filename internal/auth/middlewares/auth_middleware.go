package middlewares

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/gsoultan/panmail/pkg/cache"
)

const (
	apiKeyHeader   = "X-API-Key"
	tenantIDHeader = "X-Tenant-ID"
	rpcPathPrefix  = "/panmail.v1."
)

// Bounds on the resolved-membership cache.
//
// Acting inside a tenant that is not the one in your token costs two queries
// per request to answer "may you, and as what". The TTL is short for the same
// reason the API-key cache's is: it is the window in which a revoked
// membership still works.
const (
	membershipCacheTTL = 5 * time.Second

	// Keyed by user id and tenant id, both of which come from authenticated
	// material rather than from the request body, so the key space is bounded
	// by the number of real memberships. The limit is a backstop.
	membershipCacheMaxEntries = 4096
)

// ErrTenantNotPermitted is returned when a request names a tenant the caller
// holds no standing in.
var ErrTenantNotPermitted = errors.New("not a member of the requested tenant")

// AuthMiddleware identifies the caller and attaches a Principal to the request
// context.
//
// It rejects a credential it cannot verify rather than passing the request on
// unauthenticated: an unreadable token is an error, not an anonymous request.
// Requests carrying no credential at all are still forwarded, so that public
// procedures remain reachable; the RBAC interceptor decides whether that is
// acceptable for the procedure being called.
type AuthMiddleware struct {
	tokenMaker    auth.TokenMaker
	apiKeyUsecase usecases.ApiKeyUsecase

	// memberships answers whether a user may act in a tenant other than the
	// one their token names. Nil means membership is not configured, and only
	// a super admin can switch — the behaviour before memberships existed.
	memberships   usecases.MembershipUsecase
	resolvedRoles *cache.TTLCache[string]
}

func NewAuthMiddleware(tokenMaker auth.TokenMaker, apiKeyUsecase usecases.ApiKeyUsecase) *AuthMiddleware {
	return &AuthMiddleware{
		tokenMaker:    tokenMaker,
		apiKeyUsecase: apiKeyUsecase,
		resolvedRoles: cache.NewWithLimit[string](membershipCacheTTL, membershipCacheMaxEntries),
	}
}

// WithMemberships enables switching into any tenant the user belongs to. It is
// a separate step because the middleware is built before the database is
// necessarily up, and an instance without it still authenticates correctly.
func (m *AuthMiddleware) WithMemberships(memberships usecases.MembershipUsecase) *AuthMiddleware {
	m.memberships = memberships
	return m
}

func (m *AuthMiddleware) Handle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, rpcPathPrefix) {
			next.ServeHTTP(w, r)
			return
		}

		principal, err := m.identify(r)
		if err != nil {
			slog.Warn("rejecting request with invalid credentials", "path", r.URL.Path, "error", err)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		if principal == nil {
			next.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

// identify resolves the caller from the request. It returns (nil, nil) when no
// credential was presented, and an error when one was presented but is not
// valid.
func (m *AuthMiddleware) identify(r *http.Request) (*Principal, error) {
	if apiKey := r.Header.Get(apiKeyHeader); apiKey != "" {
		return m.identifyAPIKey(r, apiKey)
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, nil
	}
	return m.identifyBearer(r, authHeader)
}

func (m *AuthMiddleware) identifyAPIKey(r *http.Request, apiKey string) (*Principal, error) {
	if m.apiKeyUsecase == nil {
		return nil, auth.ErrInvalidToken
	}

	key, err := m.apiKeyUsecase.VerifyApiKey(r.Context(), apiKey)
	if err != nil {
		return nil, err
	}

	return &Principal{
		Kind:     PrincipalAPIKey,
		TenantID: key.TenantID,
		Scopes:   key.Scopes,
	}, nil
}

func (m *AuthMiddleware) identifyBearer(r *http.Request, authHeader string) (*Principal, error) {
	scheme, token, found := strings.Cut(authHeader, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return nil, auth.ErrInvalidToken
	}
	if m.tokenMaker == nil {
		return nil, auth.ErrInvalidToken
	}

	payload, err := m.tokenMaker.VerifyToken(token, auth.PurposeSession)
	if err != nil {
		return nil, err
	}

	principal := &Principal{
		Kind:     PrincipalUser,
		UserID:   payload.UserID,
		TenantID: payload.TenantID,
		Role:     payload.Role,
	}

	// A request may name a tenant other than the one the token was minted for.
	// The header is only consulted when it disagrees with the token, so the
	// ordinary single-tenant request costs nothing to serve.
	target := r.Header.Get(tenantIDHeader)
	if target == "" || target == payload.TenantID {
		return principal, nil
	}
	if err := m.switchTenant(r.Context(), principal, target, r.URL.Path); err != nil {
		return nil, err
	}
	return principal, nil
}

// switchTenant moves the principal into another tenant, or refuses.
//
// The role moves with it. A user who administers their own tenant and is a
// viewer in one they have been lent must be a viewer while acting there —
// carrying the home role across would make every assignment a promotion.
func (m *AuthMiddleware) switchTenant(ctx context.Context, principal *Principal, target, path string) error {
	// Super admin is global: it reaches procedures that are not scoped to any
	// tenant, so it needs no membership and is answered without a query.
	if principal.Role == RoleSuperAdmin {
		principal.TenantID = target
		slog.Info("super admin switched tenant context",
			"user_id", principal.UserID, "tenant_id", target, "path", path)
		return nil
	}

	if m.memberships == nil {
		return ErrTenantNotPermitted
	}

	role, err := m.resolveRole(ctx, principal.UserID, target)
	if err != nil {
		return err
	}
	if role == "" {
		slog.Warn("rejecting a request for a tenant the caller does not belong to",
			"user_id", principal.UserID, "tenant_id", target, "path", path)
		return ErrTenantNotPermitted
	}

	principal.TenantID = target
	principal.Role = role
	return nil
}

// resolveRole returns the role the user holds in the tenant, "" for none. A
// negative answer is cached too: a client that keeps sending a stale tenant
// header would otherwise query on every request it makes.
func (m *AuthMiddleware) resolveRole(ctx context.Context, userID, tenantID string) (string, error) {
	key := userID + "\x00" + tenantID
	if role, ok := m.resolvedRoles.Get(key); ok {
		return role, nil
	}

	role, err := m.memberships.RoleIn(ctx, userID, tenantID)
	if err != nil {
		// A user that cannot be read is not a user that may act. Do not cache
		// it: the next request should ask again rather than inherit a failure.
		return "", ErrTenantNotPermitted
	}

	m.resolvedRoles.Put(key, role)
	return role, nil
}
