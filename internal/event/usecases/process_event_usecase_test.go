package usecases

import (
	"context"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	emailstores "github.com/gsoultan/panmail/internal/email/repositories/stores"
	providerentities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	providerstores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	evententities "github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
	inboundstores "github.com/gsoultan/panmail/internal/inbound/repositories/stores"
)

type mockProviderRepo struct {
	providerstores.Repository
	providers map[string]*providerentities.EmailProvider
}

func (m *mockProviderRepo) GetByID(ctx context.Context, tenantID, id string) (*providerentities.EmailProvider, error) {
	if m.providers == nil {
		return nil, nil
	}
	return m.providers[id], nil
}

type mockEventRepo struct {
	stores.EventRepository
	events   []*evententities.EmailEvent
	messages map[string]*evententities.EmailMessage
}

func (m *mockEventRepo) Write(ctx context.Context, e *evententities.EmailEvent) error {
	m.events = append(m.events, e)
	return nil
}

func (m *mockEventRepo) WriteMessage(ctx context.Context, msg *evententities.EmailMessage) error {
	if m.messages == nil {
		m.messages = make(map[string]*evententities.EmailMessage)
	}
	m.messages[msg.ID] = msg
	return nil
}

func (m *mockEventRepo) GetMessage(ctx context.Context, tenantID string, messageID string) (*evententities.EmailMessage, error) {
	if m.messages == nil {
		return nil, nil
	}
	return m.messages[messageID], nil
}

type mockInboundRepo struct {
	inboundstores.InboundRepository
}

func (m *mockInboundRepo) Count(ctx context.Context, tenantID string, startTime, endTime time.Time) (int64, error) {
	return 0, nil
}

type mockOutboxRepo struct {
	emailstores.OutboxRepository
}

func (m *mockOutboxRepo) CountPending(ctx context.Context, tenantID string) (int64, error) {
	return 0, nil
}

func TestRecordEventRecovery(t *testing.T) {
	repo := &mockEventRepo{
		messages: make(map[string]*evententities.EmailMessage),
	}
	inboundRepo := &mockInboundRepo{}
	outboxRepo := &mockOutboxRepo{}
	providerRepo := &mockProviderRepo{
		providers: make(map[string]*providerentities.EmailProvider),
	}
	uc := NewProcessEventUsecase(repo, inboundRepo, outboxRepo, providerRepo, nil)

	tenantID := "tenant-1"
	messageID := "msg-1"
	providerID := "prov-1"
	providerName := "IMPC"
	recipient := "user@example.com"
	subject := "Test Subject"

	providerRepo.providers[providerID] = &providerentities.EmailProvider{
		ID:   providerID,
		Name: providerName,
	}

	// 1. Save a message
	repo.messages[messageID] = &evententities.EmailMessage{
		ID:         messageID,
		TenantID:   tenantID,
		To:         []string{recipient},
		ProviderID: "", // Not set yet
		Subject:    subject,
	}

	// 2. Record DELIVERED event - should update message providerID and populate event subject/providerName
	err := uc.RecordEvent(t.Context(), tenantID, providerID, messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, recipient, "", nil)
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	if repo.messages[messageID].ProviderID != providerID {
		t.Errorf("expected providerID %s, got %s", providerID, repo.messages[messageID].ProviderID)
	}

	// Find the DELIVERED event
	var deliveredEvent *evententities.EmailEvent
	for _, e := range repo.events {
		if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED {
			deliveredEvent = e
			break
		}
	}
	if deliveredEvent.Subject != subject {
		t.Errorf("expected subject %s, got %s", subject, deliveredEvent.Subject)
	}
	if deliveredEvent.ProviderName != providerName {
		t.Errorf("expected providerName %s, got %s", providerName, deliveredEvent.ProviderName)
	}

	// 3. Record OPENED event with missing providerID and recipient
	err = uc.RecordEvent(t.Context(), tenantID, "", messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED, "", "", nil)
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	// Find the OPENED event
	var openedEvent *evententities.EmailEvent
	for _, e := range repo.events {
		if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED {
			openedEvent = e
			break
		}
	}

	if openedEvent == nil {
		t.Fatal("OPENED event not found")
	}

	if openedEvent.ProviderID != providerID {
		t.Errorf("expected recovered providerID %s, got %s", providerID, openedEvent.ProviderID)
	}

	if openedEvent.Recipient != recipient {
		t.Errorf("expected recovered recipient %s, got %s", recipient, openedEvent.Recipient)
	}

	if openedEvent.Subject != subject {
		t.Errorf("expected recovered subject %s, got %s", subject, openedEvent.Subject)
	}
}
