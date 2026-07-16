package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	tenantentities "github.com/gsoultan/panmail/internal/tenant/entities"
	"google.golang.org/protobuf/encoding/protojson"
)

type workerMockOutboxRepo struct {
	emails     []*entities.OutboxEmail
	lastUpdate *entities.OutboxEmail
}

func (m *workerMockOutboxRepo) Create(ctx context.Context, e *entities.OutboxEmail) error {
	m.emails = append(m.emails, e)
	return nil
}

func (m *workerMockOutboxRepo) GetByID(ctx context.Context, id string) (*entities.OutboxEmail, error) {
	for _, e := range m.emails {
		if e.ID == id {
			return e, nil
		}
	}
	return nil, nil
}

func (m *workerMockOutboxRepo) ListPending(ctx context.Context, limit int) ([]*entities.OutboxEmail, error) {
	return m.emails, nil
}

func (m *workerMockOutboxRepo) Update(ctx context.Context, e *entities.OutboxEmail) error {
	m.lastUpdate = e
	return nil
}

func (m *workerMockOutboxRepo) Delete(ctx context.Context, id string) error {
	for i, e := range m.emails {
		if e.ID == id {
			m.emails = append(m.emails[:i], m.emails[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *workerMockOutboxRepo) CountPending(ctx context.Context, tenantID string) (int64, error) {
	return int64(len(m.emails)), nil
}

type mockEmailUsecase struct {
	err   error
	delay time.Duration
}

func (m *mockEmailUsecase) SendEmail(ctx context.Context, tenantID string, req *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	return &panmailv1.SendEmailResponse{MessageId: "123", Status: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED}, nil
}

func (m *mockEmailUsecase) RecordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient string, errorMessage string, metadata map[string]any) error {
	return nil
}

func (m *mockEmailUsecase) RegisterQueueWorker(w QueueWorker) {}

type mockSuppressionUsecase struct {
	suppressedEmails []string
}

func (m *mockSuppressionUsecase) Add(ctx context.Context, tenantID string, req *panmailv1.AddSuppressionRequest) (*panmailv1.Suppression, error) {
	m.suppressedEmails = append(m.suppressedEmails, req.Email)
	return &panmailv1.Suppression{Email: req.Email}, nil
}

func (m *mockSuppressionUsecase) Remove(ctx context.Context, tenantID, email string) error {
	return nil
}
func (m *mockSuppressionUsecase) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.Suppression, string, error) {
	return nil, "", nil
}
func (m *mockSuppressionUsecase) Check(ctx context.Context, tenantID, email string) (bool, string, error) {
	return false, "", nil
}

type mockTenantUsecase struct{}

func (m *mockTenantUsecase) CreateTenant(ctx context.Context, name string, retryPattern []string) (*tenantentities.Tenant, error) {
	return nil, nil
}
func (m *mockTenantUsecase) ListTenants(ctx context.Context, pageSize int, pageToken string) ([]*tenantentities.Tenant, string, error) {
	return nil, "", nil
}
func (m *mockTenantUsecase) GetTenantByID(ctx context.Context, id string) (*tenantentities.Tenant, error) {
	return &tenantentities.Tenant{ID: id}, nil
}
func (m *mockTenantUsecase) UpdateTenant(ctx context.Context, id string, name string, retryPattern []string) (*tenantentities.Tenant, error) {
	return nil, nil
}
func (m *mockTenantUsecase) DeleteTenant(ctx context.Context, id string) error {
	return nil
}

func TestQueueWorker_ProcessEmail(t *testing.T) {
	tests := []struct {
		name               string
		lastError          error
		expectedStatus     entities.OutboxStatus
		expectedRetryCount int
		expectSuppressed   bool
	}{
		{
			name:               "Soft Bounce - Should Retry",
			lastError:          errors.New("421 4.3.0 Temporary failure"),
			expectedStatus:     entities.OutboxStatusDeferred,
			expectedRetryCount: 1,
			expectSuppressed:   false,
		},
		{
			name:               "Hard Bounce - Should Fail and Suppress",
			lastError:          errors.New("550 5.1.1 User unknown"),
			expectedStatus:     entities.OutboxStatusFailed,
			expectedRetryCount: 1,
			expectSuppressed:   true,
		},
		{
			name:               "Success - Should Delete",
			lastError:          nil,
			expectedStatus:     "", // N/A, deleted
			expectedRetryCount: 0,
			expectSuppressed:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outboxRepo := &workerMockOutboxRepo{}
			emailUsecase := &mockEmailUsecase{err: tc.lastError}
			suppressionUsecase := &mockSuppressionUsecase{}
			tenantUsecase := &mockTenantUsecase{}

			w := NewQueueWorker(outboxRepo, emailUsecase, suppressionUsecase, tenantUsecase, 1*time.Second).(*queueWorker)

			req := panmailv1.SendEmailRequest{
				To:      []string{"test@example.com"},
				From:    "sender@example.com",
				Subject: "Test",
				Body:    "Hello",
			}
			reqBytes, _ := protojson.Marshal(&req)

			email := &entities.OutboxEmail{
				ID:          "123",
				TenantID:    "tenant1",
				Request:     reqBytes,
				Status:      entities.OutboxStatusPending,
				RetryCount:  0,
				NextRetryAt: time.Now(),
			}

			w.processEmail(context.Background(), email)

			if tc.lastError == nil {
				if len(outboxRepo.emails) != 0 {
					t.Errorf("expected outbox to be empty after success, got %d", len(outboxRepo.emails))
				}
				return
			}

			if outboxRepo.lastUpdate.Status != tc.expectedStatus {
				t.Errorf("expected status %s, got %s", tc.expectedStatus, outboxRepo.lastUpdate.Status)
			}

			if outboxRepo.lastUpdate.RetryCount != tc.expectedRetryCount {
				t.Errorf("expected retry count %d, got %d", tc.expectedRetryCount, outboxRepo.lastUpdate.RetryCount)
			}

			if tc.expectSuppressed {
				if len(suppressionUsecase.suppressedEmails) == 0 {
					t.Error("expected recipient to be suppressed")
				}
			}
		})
	}
}
