package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	evententities "github.com/gsoultan/panmail/internal/event/repositories/entities"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
	suppressionentities "github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	templateentities "github.com/gsoultan/panmail/internal/template/repositories/entities"
	"google.golang.org/protobuf/types/known/structpb"
)

type mockProviderRepo struct {
	provider *providerEntities.EmailProvider
}

func (m *mockProviderRepo) Create(ctx context.Context, p *providerEntities.EmailProvider) error {
	return nil
}
func (m *mockProviderRepo) GetByID(ctx context.Context, tenantID, id string) (*providerEntities.EmailProvider, error) {
	return m.provider, nil
}
func (m *mockProviderRepo) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*providerEntities.EmailProvider, string, error) {
	if m.provider != nil {
		return []*providerEntities.EmailProvider{m.provider}, "", nil
	}
	return nil, "", nil
}
func (m *mockProviderRepo) Update(ctx context.Context, p *providerEntities.EmailProvider) error {
	return nil
}
func (m *mockProviderRepo) Delete(ctx context.Context, tenantID, id string) error { return nil }

type mockOutboxRepo struct {
	emails []*entities.OutboxEmail
}

func (m *mockOutboxRepo) Create(ctx context.Context, e *entities.OutboxEmail) error {
	m.emails = append(m.emails, e)
	return nil
}
func (m *mockOutboxRepo) GetByID(ctx context.Context, id string) (*entities.OutboxEmail, error) {
	return nil, nil
}
func (m *mockOutboxRepo) ListPending(ctx context.Context, limit int) ([]*entities.OutboxEmail, error) {
	return nil, nil
}
func (m *mockOutboxRepo) Update(ctx context.Context, e *entities.OutboxEmail) error { return nil }
func (m *mockOutboxRepo) Delete(ctx context.Context, id string) error               { return nil }

type mockEventRepo struct {
	events []*evententities.EmailEvent
}

func (m *mockEventRepo) Write(ctx context.Context, e *evententities.EmailEvent) error {
	m.events = append(m.events, e)
	return nil
}
func (m *mockEventRepo) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*evententities.EmailEvent, string, error) {
	return nil, "", nil
}
func (m *mockEventRepo) GetByID(ctx context.Context, tenantID, id string) (*evententities.EmailEvent, error) {
	return nil, nil
}
func (m *mockEventRepo) GetMetrics(ctx context.Context, tenantID string) (map[string]int64, error) {
	return nil, nil
}
func (m *mockEventRepo) GetTimeSeriesMetrics(ctx context.Context, tenantID string) (map[string]map[string]int64, error) {
	return nil, nil
}
func (m *mockEventRepo) Close() error { return nil }

type mockEventUsecase struct {
	events   []*evententities.EmailEvent
	messages []*panmailv1.EmailMessage
}

func (m *mockEventUsecase) RecordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient string, errorMessage string, metadata map[string]any) error {
	m.events = append(m.events, &evententities.EmailEvent{
		TenantID:     tenantID,
		ProviderID:   providerID,
		MessageID:    messageID,
		Type:         eventType,
		Recipient:    recipient,
		Metadata:     metadata,
		ErrorMessage: errorMessage,
	})
	return nil
}
func (m *mockEventUsecase) ListEvents(ctx context.Context, tenantID string, filter eventusecases.ListFilter) ([]*panmailv1.EmailEvent, string, error) {
	return nil, "", nil
}
func (m *mockEventUsecase) GetMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time) (map[string]int64, []*panmailv1.MetricInfo, error) {
	return nil, nil, nil
}
func (m *mockEventUsecase) GetTimeSeriesMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time, granularity string) (map[string]map[string]int64, error) {
	return nil, nil
}
func (m *mockEventUsecase) GetEvent(ctx context.Context, tenantID string, id string) (*panmailv1.EmailEvent, *panmailv1.EmailMessage, error) {
	return nil, nil, nil
}
func (m *mockEventUsecase) SaveMessage(ctx context.Context, message *panmailv1.EmailMessage) error {
	m.messages = append(m.messages, message)
	return nil
}
func (m *mockEventUsecase) StartCleanupTask(ctx context.Context, interval time.Duration, retentionDays int) {
}
func (m *mockEventUsecase) GetPerformanceMetrics(ctx context.Context) (eventusecases.PerformanceMetrics, error) {
	return eventusecases.PerformanceMetrics{}, nil
}
func (m *mockEventUsecase) ListArchives(ctx context.Context, pageSize int, pageToken string) ([]evententities.ArchiveInfo, string, error) {
	return nil, "", nil
}
func (m *mockEventUsecase) GetArchive(ctx context.Context, id string) ([]byte, string, error) {
	return nil, "", nil
}

type mockTemplateRepo struct {
	template *templateentities.Template
}

func (m *mockTemplateRepo) Create(ctx context.Context, t *templateentities.Template) error {
	return nil
}
func (m *mockTemplateRepo) GetByID(ctx context.Context, tenantID, id string) (*templateentities.Template, error) {
	return m.template, nil
}
func (m *mockTemplateRepo) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*templateentities.Template, string, error) {
	return nil, "", nil
}
func (m *mockTemplateRepo) Update(ctx context.Context, t *templateentities.Template) error {
	return nil
}
func (m *mockTemplateRepo) Delete(ctx context.Context, tenantID, id string) error { return nil }

type mockSuppressionRepo struct {
	suppression *suppressionentities.Suppression
}

func (m *mockSuppressionRepo) Create(ctx context.Context, s *suppressionentities.Suppression) error {
	return nil
}
func (m *mockSuppressionRepo) Delete(ctx context.Context, tenantID, email string) error { return nil }
func (m *mockSuppressionRepo) GetByEmail(ctx context.Context, tenantID, email string) (*suppressionentities.Suppression, error) {
	return m.suppression, nil
}
func (m *mockSuppressionRepo) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*suppressionentities.Suppression, string, error) {
	return nil, "", nil
}

type mockFactory struct {
	sender any
	err    error
}

func (m *mockFactory) CreateSender(p *providerEntities.EmailProvider) (any, error) {
	return m.sender, m.err
}
func (m *mockFactory) CreateReceiver(p *providerEntities.EmailProvider) (any, error) {
	return nil, nil
}

type mockSender struct {
	err error
}

func (m *mockSender) Send(ctx context.Context, email gsmail.Email) error {
	return m.err
}
func (m *mockSender) Validate(ctx context.Context, email string) error { return nil }
func (m *mockSender) Ping(ctx context.Context) error                   { return nil }
func (m *mockSender) SetRetryConfig(config gsmail.RetryConfig)         {}

func TestSendEmailUsecase_SendEmail(t *testing.T) {
	tests := []struct {
		name     string
		provider *providerEntities.EmailProvider
		template *templateentities.Template
		sender   *mockSender
		req      *panmailv1.SendEmailRequest
		wantSub  string
		wantHTML string
		wantErr  bool
	}{
		{
			name: "Successful Send",
			provider: &providerEntities.EmailProvider{
				ID:   "p1",
				Name: "SMTP Provider",
				Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
			},
			sender: &mockSender{},
			req: &panmailv1.SendEmailRequest{
				ProviderId: "p1",
				From:       "from@example.com",
				To:         []string{"to@example.com"},
				Subject:    "Hello",
				Body:       "World",
			},
			wantSub: "Hello",
			wantErr: false,
		},
		{
			name: "Templated Send",
			provider: &providerEntities.EmailProvider{
				ID:   "p1",
				Name: "SMTP Provider",
				Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
			},
			sender: &mockSender{},
			template: &templateentities.Template{
				ID:       "t1",
				Subject:  "Hello {{name}}",
				BodyHTML: "<p>Welcome to {{product}}</p>",
			},
			req: &panmailv1.SendEmailRequest{
				ProviderId: "p1",
				From:       "from@example.com",
				TemplateId: "t1",
				TemplateData: func() *structpb.Struct {
					s, _ := structpb.NewStruct(map[string]any{"name": "Junie", "product": "Panmail"})
					return s
				}(),
			},
			wantSub:  "Hello Junie",
			wantHTML: "<p>Welcome to Panmail</p>",
			wantErr:  false,
		},
		{
			name: "Go-style Template Variables",
			provider: &providerEntities.EmailProvider{
				ID:   "p1",
				Name: "SMTP Provider",
				Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
			},
			sender: &mockSender{},
			template: &templateentities.Template{
				ID:       "t2",
				Subject:  "Hello {{.name}}",
				BodyHTML: "<p>Welcome to {{.product}}</p>",
			},
			req: &panmailv1.SendEmailRequest{
				ProviderId: "p1",
				From:       "from@example.com",
				TemplateId: "t2",
				TemplateData: func() *structpb.Struct {
					s, _ := structpb.NewStruct(map[string]any{"name": "Junie", "product": "Panmail"})
					return s
				}(),
			},
			wantSub:  "Hello Junie",
			wantHTML: "<p>Welcome to Panmail</p>",
			wantErr:  false,
		},
		{
			name:     "Provider Not Found",
			provider: nil,
			sender:   &mockSender{},
			req: &panmailv1.SendEmailRequest{
				ProviderId: "invalid",
			},
			wantErr: true,
		},
		{
			name: "Send Error",
			provider: &providerEntities.EmailProvider{
				ID:   "p1",
				Name: "SMTP Provider",
				Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
			},
			sender: &mockSender{err: errors.New("smtp error")},
			req: &panmailv1.SendEmailRequest{
				ProviderId: "p1",
				From:       "from@example.com",
				To:         []string{"to@example.com"},
			},
			wantErr: false, // Now returns res with PENDING status, not error
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockProviderRepo{provider: tc.provider}
			templateRepo := &mockTemplateRepo{template: tc.template}
			suppressionRepo := &mockSuppressionRepo{}
			eventUsecase := &mockEventUsecase{}
			outboxRepo := &mockOutboxRepo{}
			factory := &mockFactory{sender: tc.sender}
			renderer := NewTemplateRenderer()
			u := NewSendEmailUsecase(repo, templateRepo, suppressionRepo, outboxRepo, eventUsecase, factory, renderer, "http://localhost")

			res, err := u.SendEmail(context.Background(), "test-tenant", tc.req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("SendEmail() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && !tc.wantErr {
				if len(outboxRepo.emails) == 0 {
					t.Error("expected email to be queued in outbox")
				} else {
					if res.Status != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING {
						t.Errorf("expected status PENDING, got %v", res.Status)
					}
				}
			}
			if err == nil && tc.wantSub != "" {
				if len(eventUsecase.messages) == 0 {
					t.Fatalf("expected message to be saved")
				}
				msg := eventUsecase.messages[0]
				if msg.Subject != tc.wantSub {
					t.Errorf("expected subject %q, got %q", tc.wantSub, msg.Subject)
				}
				if tc.wantHTML != "" && msg.BodyHtml != tc.wantHTML {
					// Note: injectTracking might add pixels, so we check for containment if needed
					// But in this mock tracking might be disabled if baseURL is empty or not matching.
					// In NewSendEmailUsecase above we pass "http://localhost".
					if !strings.Contains(msg.BodyHtml, tc.wantHTML) {
						t.Errorf("expected HTML to contain %q, got %q", tc.wantHTML, msg.BodyHtml)
					}
				}
			}
		})
	}
}
