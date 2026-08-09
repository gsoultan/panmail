package middlewares

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/pkg/auth"
)

type mockApiKeyUsecase struct {
	key *entities.ApiKey
	err error
}

func (m *mockApiKeyUsecase) CreateApiKey(ctx context.Context, req usecases.NewApiKey) (*entities.ApiKey, string, error) {
	return nil, "", nil
}
func (m *mockApiKeyUsecase) ListApiKeys(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.ApiKey, string, error) {
	return nil, "", nil
}
func (m *mockApiKeyUsecase) DeleteApiKey(ctx context.Context, id, tenantID string) error { return nil }
func (m *mockApiKeyUsecase) DisableApiKey(ctx context.Context, id, tenantID string) error {
	return nil
}
func (m *mockApiKeyUsecase) EnableApiKey(ctx context.Context, id, tenantID string) error { return nil }
func (m *mockApiKeyUsecase) VerifyApiKey(ctx context.Context, key string) (*entities.ApiKey, error) {
	return m.key, m.err
}

func newTestTokenMaker(t *testing.T) auth.TokenMaker {
	t.Helper()
	maker, err := auth.NewPasetoMaker("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("failed to create token maker: %v", err)
	}
	return maker
}

func TestAuthMiddleware_ApiKeyCarriesScopesNotARole(t *testing.T) {
	mockUsecase := &mockApiKeyUsecase{
		key: &entities.ApiKey{
			TenantID: "test-tenant",
			Scopes:   []entities.Scope{entities.ScopeEmailSend},
		},
	}
	middleware := NewAuthMiddleware(nil, mockUsecase)

	var seen *Principal
	handler := middleware.Handle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := GetPrincipal(r.Context())
		if !ok {
			t.Error("expected a principal on the context")
		}
		seen = p
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/panmail.v1.EmailService/SendEmail", nil)
	req.Header.Set(apiKeyHeader, "pm_testkey")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected OK, got %d", rec.Code)
	}
	if seen == nil {
		t.Fatal("no principal captured")
	}
	if seen.Kind != PrincipalAPIKey {
		t.Errorf("expected an api key principal, got %s", seen.Kind)
	}
	if seen.TenantID != "test-tenant" {
		t.Errorf("expected tenant test-tenant, got %s", seen.TenantID)
	}
	// An API key must not inherit a user role: that is what previously let a
	// send-only key manage providers, templates and webhooks.
	if seen.Role != "" {
		t.Errorf("expected no role on an api key principal, got %q", seen.Role)
	}
	if !seen.HasScope(entities.ScopeEmailSend) {
		t.Error("expected the email:send scope to be carried")
	}
	if seen.HasScope(entities.ScopeProvidersWrite) {
		t.Error("api key must not hold scopes it was never granted")
	}
}

// A credential that cannot be verified is an error, not an anonymous request.
func TestAuthMiddleware_RejectsBadCredentials(t *testing.T) {
	maker := newTestTokenMaker(t)

	tests := []struct {
		name    string
		header  string
		value   string
		usecase *mockApiKeyUsecase
	}{
		{"garbage bearer token", "Authorization", "Bearer not-a-real-token", nil},
		{"malformed authorization header", "Authorization", "Bearer", nil},
		{"unknown api key", apiKeyHeader, "pm_nope", &mockApiKeyUsecase{err: errors.New("not found")}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var keyUsecase usecases.ApiKeyUsecase
			if tc.usecase != nil {
				keyUsecase = tc.usecase
			}
			middleware := NewAuthMiddleware(maker, keyUsecase)

			handler := middleware.Handle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("handler must not run for an unverifiable credential")
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("POST", "/panmail.v1.EmailService/SendEmail", nil)
			req.Header.Set(tc.header, tc.value)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rec.Code)
			}
		})
	}
}

// A two-factor challenge token proves only that the password step passed. It
// must not be usable as an API session.
func TestAuthMiddleware_RejectsChallengeTokenAsSession(t *testing.T) {
	maker := newTestTokenMaker(t)

	challenge, err := maker.CreateToken(auth.TokenRequest{
		UserID:   "user-1",
		TenantID: "tenant-1",
		Role:     RoleSuperAdmin,
		Purpose:  auth.PurposeTwoFactorChallenge,
		Duration: time.Minute,
	})
	if err != nil {
		t.Fatalf("failed to create challenge token: %v", err)
	}

	middleware := NewAuthMiddleware(maker, nil)
	handler := middleware.Handle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a challenge token must not authenticate an API call")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/panmail.v1.TenantService/ListTenants", nil)
	req.Header.Set("Authorization", "Bearer "+challenge)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// Non-RPC paths (the SPA, tracking pixels, provider webhooks) are not touched.
func TestAuthMiddleware_PassesThroughNonRPCPaths(t *testing.T) {
	middleware := NewAuthMiddleware(newTestTokenMaker(t), nil)

	called := false
	handler := middleware.Handle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/track/open/tenant/message", nil)
	req.Header.Set("Authorization", "Bearer garbage")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected non-RPC paths to pass through untouched")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected OK, got %d", rec.Code)
	}
}
