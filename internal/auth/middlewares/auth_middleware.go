package middlewares

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/pkg/auth"
)

const (
	apiKeyHeader   = "X-API-Key"
	tenantIDHeader = "X-Tenant-ID"
	rpcPathPrefix  = "/panmail.v1."
)

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
}

func NewAuthMiddleware(tokenMaker auth.TokenMaker, apiKeyUsecase usecases.ApiKeyUsecase) *AuthMiddleware {
	return &AuthMiddleware{
		tokenMaker:    tokenMaker,
		apiKeyUsecase: apiKeyUsecase,
	}
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

	// A super admin may act inside another tenant for support and monitoring.
	if payload.Role == RoleSuperAdmin {
		if target := r.Header.Get(tenantIDHeader); target != "" {
			principal.TenantID = target
			slog.Info("super admin switched tenant context",
				"user_id", payload.UserID, "tenant_id", target, "path", r.URL.Path)
		}
	}

	return principal, nil
}
