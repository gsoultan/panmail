package usecases

import (
	"context"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
)

func TestManageProvidersUsecase_Validation(t *testing.T) {
	repo := &mockRepo{providers: make(map[string]*entities.EmailProvider)}
	factory := NewProviderFactory()
	u := NewManageProvidersUsecase(repo, factory)
	tenantID := "00000000-0000-0000-0000-000000000001"

	t.Run("Get Invalid ID", func(t *testing.T) {
		_, err := u.Get(context.Background(), tenantID, "default")
		if err == nil || !strings.Contains(err.Error(), "invalid provider id format") {
			t.Errorf("expected validation error for invalid ID, got %v", err)
		}
	})

	t.Run("Update Invalid ID", func(t *testing.T) {
		_, err := u.Update(context.Background(), tenantID, &panmailv1.UpdateEmailProviderRequest{
			Id:   "default",
			Name: "Invalid",
		})
		if err == nil || !strings.Contains(err.Error(), "invalid provider id format") {
			t.Errorf("expected validation error for invalid ID, got %v", err)
		}
	})

	t.Run("Delete Invalid ID", func(t *testing.T) {
		err := u.Delete(context.Background(), tenantID, "default")
		if err == nil || !strings.Contains(err.Error(), "invalid provider id format") {
			t.Errorf("expected validation error for invalid ID, got %v", err)
		}
	})

	t.Run("Test Invalid ID", func(t *testing.T) {
		err := u.Test(context.Background(), tenantID, "default")
		if err == nil || !strings.Contains(err.Error(), "invalid provider id format") {
			t.Errorf("expected validation error for invalid ID, got %v", err)
		}
	})
}
