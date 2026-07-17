package usecases

import (
	"context"
	"testing"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

func TestSendEmail_Validation(t *testing.T) {
	uc := &sendEmailUsecase{}

	validTenantID := uuid.New().String()
	validProviderID := uuid.New().String()

	tests := []struct {
		name     string
		tenantID string
		req      *panmailv1.SendEmailRequest
		wantErr  bool
		errMatch string
	}{
		{
			name:     "Empty Tenant ID",
			tenantID: "",
			req: &panmailv1.SendEmailRequest{
				ProviderId: validProviderID,
				From:       "test@example.com",
				To:         []string{"to@example.com"},
			},
			wantErr:  true,
			errMatch: "tenant id is mandatory",
		},
		{
			name:     "Invalid Tenant ID Format",
			tenantID: "not-a-uuid",
			req: &panmailv1.SendEmailRequest{
				ProviderId: validProviderID,
				From:       "test@example.com",
				To:         []string{"to@example.com"},
			},
			wantErr:  true,
			errMatch: "invalid tenant id format",
		},
		{
			name:     "Nil Tenant ID",
			tenantID: uuid.Nil.String(),
			req: &panmailv1.SendEmailRequest{
				ProviderId: validProviderID,
				From:       "test@example.com",
				To:         []string{"to@example.com"},
			},
			wantErr:  true,
			errMatch: "tenant id cannot be nil uuid",
		},
		{
			name:     "Empty Provider ID",
			tenantID: validTenantID,
			req: &panmailv1.SendEmailRequest{
				ProviderId: "",
				From:       "test@example.com",
				To:         []string{"to@example.com"},
			},
			wantErr:  true,
			errMatch: "provider id is mandatory",
		},
		{
			name:     "Invalid Provider ID Format (default)",
			tenantID: validTenantID,
			req: &panmailv1.SendEmailRequest{
				ProviderId: "default",
				From:       "test@example.com",
				To:         []string{"to@example.com"},
			},
			wantErr:  true,
			errMatch: "invalid provider id format",
		},
		{
			name:     "Nil Provider ID",
			tenantID: validTenantID,
			req: &panmailv1.SendEmailRequest{
				ProviderId: uuid.Nil.String(),
				From:       "test@example.com",
				To:         []string{"to@example.com"},
			},
			wantErr:  true,
			errMatch: "provider id cannot be nil uuid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := uc.SendEmail(context.Background(), tc.tenantID, tc.req)
			if (err != nil) != tc.wantErr {
				t.Errorf("SendEmail() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if tc.wantErr && err != nil {
				if !contains(err.Error(), tc.errMatch) {
					t.Errorf("SendEmail() error = %v, want error containing %q", err, tc.errMatch)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(substr) > 0 && (s[:len(substr)] == substr || contains(s[1:], substr))))
}
