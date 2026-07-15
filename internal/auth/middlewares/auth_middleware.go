package middlewares

import (
	"context"
	"net/http"
	"strings"

	"github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/pkg/auth"
)

type ContextKey string

const (
	UserIDKey   ContextKey = "user_id"
	TenantIDKey ContextKey = "tenant_id"
	RoleKey     ContextKey = "role"
)

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
		path := r.URL.Path

		// Only protect API paths
		if !strings.HasPrefix(path, "/panmail.v1.") {
			next.ServeHTTP(w, r)
			return
		}

		// Skip auth for Setup and SignIn
		if strings.Contains(path, "panmail.v1.AuthService/SignIn") ||
			strings.Contains(path, "panmail.v1.SetupService") {
			next.ServeHTTP(w, r)
			return
		}

		// Check for API Key first (usually for external applications)
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "" && m.apiKeyUsecase != nil {
			key, err := m.apiKeyUsecase.VerifyApiKey(r.Context(), apiKey)
			if err == nil {
				ctx := context.WithValue(r.Context(), TenantIDKey, key.TenantID)
				// API Key defaults to Editor role for simplicity, or we could store it in DB
				ctx = context.WithValue(ctx, RoleKey, "USER_ROLE_EDITOR")
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			next.ServeHTTP(w, r)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			next.ServeHTTP(w, r)
			return
		}

		token := parts[1]
		if m.tokenMaker == nil {
			next.ServeHTTP(w, r)
			return
		}
		payload, err := m.tokenMaker.VerifyToken(token)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), UserIDKey, payload.UserID)
		ctx = context.WithValue(ctx, TenantIDKey, payload.TenantID)
		ctx = context.WithValue(ctx, RoleKey, payload.Role)

		// Super Admin can switch tenants via X-Tenant-ID header
		if payload.Role == "USER_ROLE_SUPER_ADMIN" {
			targetTenantID := r.Header.Get("X-Tenant-ID")
			if targetTenantID != "" {
				ctx = context.WithValue(ctx, TenantIDKey, targetTenantID)
			}
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GetRole(ctx context.Context) string {
	if role, ok := ctx.Value(RoleKey).(string); ok {
		return role
	}
	return ""
}

var roleLevel = map[string]int{
	"USER_ROLE_VIEWER":      1,
	"USER_ROLE_EDITOR":      2,
	"USER_ROLE_ADMIN":       3,
	"USER_ROLE_SUPER_ADMIN": 4,
}

func HasRole(ctx context.Context, requiredRoles ...string) bool {
	userRole := GetRole(ctx)
	userLvl := roleLevel[userRole]

	for _, r := range requiredRoles {
		reqLvl := roleLevel[r]
		if userLvl >= reqLvl && reqLvl > 0 {
			return true
		}
	}
	return false
}
