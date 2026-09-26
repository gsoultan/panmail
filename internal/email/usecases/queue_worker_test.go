package usecases

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	suppressionusecases "github.com/gsoultan/panmail/internal/suppression/usecases"
	tenantentities "github.com/gsoultan/panmail/internal/tenant/entities"
	tenantusecases "github.com/gsoultan/panmail/internal/tenant/usecases"
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
	err      error
	delay    time.Duration
	recorded []panmailv1.EmailEventType
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

func (m *mockEmailUsecase) RecordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient, subject, errorMessage string, metadata map[string]any) error {
	m.recorded = append(m.recorded, eventType)
	return nil
}

func (m *mockEmailUsecase) RegisterQueueWorker(w QueueWorker) {}

type mockSuppressionUsecase struct {
	suppressedEmails []string
}

func (m *mockSuppressionUsecase) Import(context.Context, string, []*panmailv1.SuppressionEntry) (suppressionusecases.ImportResult, error) {
	return suppressionusecases.ImportResult{}, nil
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

func (m *mockTenantUsecase) CreateTenant(ctx context.Context, name string, retryPattern []string, limits tenantusecases.SendLimits) (*tenantentities.Tenant, error) {
	return nil, nil
}
func (m *mockTenantUsecase) ListTenants(ctx context.Context, pageSize int, pageToken string) ([]*tenantentities.Tenant, string, error) {
	return nil, "", nil
}
func (m *mockTenantUsecase) GetTenantByID(ctx context.Context, id string) (*tenantentities.Tenant, error) {
	return &tenantentities.Tenant{ID: id}, nil
}
func (m *mockTenantUsecase) UpdateTenant(ctx context.Context, id string, name string, retryPattern []string, limits tenantusecases.SendLimits) (*tenantentities.Tenant, error) {
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
		// expectedEvent is what the worker hands to the event pipeline. A hard
		// bounce is suppressed there rather than here -- see below.
		expectedEvent panmailv1.EmailEventType
	}{
		{
			name:               "Soft Bounce - Should Retry",
			lastError:          errors.New("421 4.3.0 Temporary failure"),
			expectedStatus:     entities.OutboxStatusDeferred,
			expectedRetryCount: 1,
			expectedEvent:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DEFERRED,
		},
		{
			name:               "Hard Bounce - Should Fail and Suppress",
			lastError:          errors.New("550 5.1.1 User unknown"),
			expectedStatus:     entities.OutboxStatusFailed,
			expectedRetryCount: 1,
			expectedEvent:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
		},
		{
			name:               "Success - Should Delete",
			lastError:          nil,
			expectedStatus:     "", // N/A, deleted
			expectedRetryCount: 0,
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

			if tc.expectedEvent != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
				if !slices.Contains(emailUsecase.recorded, tc.expectedEvent) {
					t.Errorf("recorded %v, want it to include %v", emailUsecase.recorded, tc.expectedEvent)
				}
			}

			// Suppression moved to RecordEvent, so that a bounce reported by an
			// ESP webhook or an inbound DSN suppresses the address too, not
			// only one seen by this worker. What is left here is the
			// unsubscribe an SMTP error can carry, which no other path
			// produces -- so a hard bounce must no longer suppress from here,
			// or every address would be added twice.
			if len(suppressionUsecase.suppressedEmails) != 0 {
				t.Errorf("worker suppressed %v directly; that belongs to the event pipeline now",
					suppressionUsecase.suppressedEmails)
			}
		})
	}
}

func (m *workerMockOutboxRepo) ClaimPending(ctx context.Context, limit int, leaseFor time.Duration) ([]*entities.OutboxEmail, error) {
	return m.ListPending(ctx, limit)
}

func (m *workerMockOutboxRepo) PruneTerminal(ctx context.Context, olderThan time.Time) (int64, error) {
	var removed int64
	kept := m.emails[:0]
	for _, e := range m.emails {
		if e.Status == entities.OutboxStatusFailed && e.UpdatedAt.Before(olderThan) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	m.emails = kept
	return removed, nil
}

func (m *workerMockOutboxRepo) Stats(context.Context) (int64, time.Time, error) {
	oldest := time.Now()
	for _, e := range m.emails {
		if e.CreatedAt.Before(oldest) {
			oldest = e.CreatedAt
		}
	}
	return int64(len(m.emails)), oldest, nil
}

// A domain refusal read as retryable, so it went round the whole default
// schedule -- eight retries over roughly two days -- failing identically each
// time. A refusal that says it is permanent fails on the first attempt.
func TestAPermanentRefusalIsNotRetried(t *testing.T) {
	outboxRepo := &workerMockOutboxRepo{}
	emailUsecase := &mockEmailUsecase{
		err: &ProviderDomainRefusedError{Provider: "ESP", Domain: "someone-elses-bank.com"},
	}
	w := NewQueueWorker(outboxRepo, emailUsecase, &mockSuppressionUsecase{},
		&mockTenantUsecase{}, time.Second).(*queueWorker)

	req := panmailv1.SendEmailRequest{
		To: []string{"victim@example.com"}, From: "ceo@someone-elses-bank.com",
		Subject: "x", Body: "x",
	}
	reqBytes, _ := protojson.Marshal(&req)
	outboxRepo.emails = append(outboxRepo.emails, &entities.OutboxEmail{
		ID: "123", TenantID: "tenant1", Request: reqBytes,
		Status: entities.OutboxStatusPending, NextRetryAt: time.Now(),
	})

	w.processPending(context.Background())

	if outboxRepo.lastUpdate == nil {
		t.Fatal("the outbox row was never updated")
	}
	if outboxRepo.lastUpdate.Status != entities.OutboxStatusFailed {
		t.Fatalf("status = %s, want failed on the first attempt rather than deferred",
			outboxRepo.lastUpdate.Status)
	}
	if outboxRepo.lastUpdate.RetryCount != 1 {
		t.Errorf("retry count = %d, want 1 attempt", outboxRepo.lastUpdate.RetryCount)
	}
}

// The marker is read from the error, not its text: wrapping it on the way up
// must not quietly turn the retries back on.
func TestAPermanentRefusalSurvivesWrapping(t *testing.T) {
	outboxRepo := &workerMockOutboxRepo{}
	emailUsecase := &mockEmailUsecase{
		err: fmt.Errorf("delivery failed: %w",
			&ProviderDomainRefusedError{Provider: "ESP", Domain: "example.com"}),
	}
	w := NewQueueWorker(outboxRepo, emailUsecase, &mockSuppressionUsecase{},
		&mockTenantUsecase{}, time.Second).(*queueWorker)

	req := panmailv1.SendEmailRequest{To: []string{"a@example.com"}, From: "b@example.com", Subject: "x", Body: "x"}
	reqBytes, _ := protojson.Marshal(&req)
	outboxRepo.emails = append(outboxRepo.emails, &entities.OutboxEmail{
		ID: "124", TenantID: "tenant1", Request: reqBytes,
		Status: entities.OutboxStatusPending, NextRetryAt: time.Now(),
	})

	w.processPending(context.Background())

	if outboxRepo.lastUpdate == nil || outboxRepo.lastUpdate.Status != entities.OutboxStatusFailed {
		t.Fatalf("a wrapped permanent refusal was retried: %+v", outboxRepo.lastUpdate)
	}
}
