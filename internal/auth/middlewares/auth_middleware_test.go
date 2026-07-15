package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
)

type mockApiKeyUsecase struct {
	key *entities.ApiKey
	err error
}

func (m *mockApiKeyUsecase) CreateApiKey(ctx context.Context, tenantID, name string, expiresAt *time.Time) (*entities.ApiKey, string, error) {
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

func TestAuthMiddleware_ApiKey(t *testing.T) {
	mockUsecase := &mockApiKeyUsecase{
		key: &entities.ApiKey{
			TenantID: "test-tenant",
		},
	}
	middleware := NewAuthMiddleware(nil, mockUsecase)

	handler := middleware.Handle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Context().Value(TenantIDKey).(string)
		role := r.Context().Value(RoleKey).(string)
		if tenantID != "test-tenant" {
			t.Errorf("expected tenant-tenant, got %s", tenantID)
		}
		if role != "USER_ROLE_EDITOR" {
			t.Errorf("expected USER_ROLE_EDITOR, got %s", role)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/panmail.v1.EmailService/SendEmail", nil)
	req.Header.Set("X-API-Key", "pm_testkey")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected OK, got %d", rec.Code)
	}
}
