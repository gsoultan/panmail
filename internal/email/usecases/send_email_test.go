package usecases

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"google.golang.org/protobuf/types/known/timestamppb"
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
func (m *mockOutboxRepo) CountPending(ctx context.Context, tenantID string) (int64, error) {
	return int64(len(m.emails)), nil
}

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

func (m *mockEventUsecase) RecordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient string, subject string, errorMessage string, metadata map[string]any) error {
	m.events = append(m.events, &evententities.EmailEvent{
		TenantID:     tenantID,
		ProviderID:   providerID,
		MessageID:    messageID,
		Type:         eventType,
		Recipient:    recipient,
		Subject:      subject,
		Metadata:     metadata,
		ErrorMessage: errorMessage,
		Timestamp:    time.Now(),
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
func (m *mockEventUsecase) ListByMessageID(ctx context.Context, tenantID string, messageID string) ([]*panmailv1.EmailEvent, error) {
	var res []*panmailv1.EmailEvent
	for _, e := range m.events {
		if e.MessageID == messageID {
			meta, _ := structpb.NewStruct(e.Metadata)
			res = append(res, &panmailv1.EmailEvent{
				Id:           e.ID,
				TenantId:     e.TenantID,
				ProviderId:   e.ProviderID,
				MessageId:    e.MessageID,
				Type:         e.Type,
				Recipient:    e.Recipient,
				Timestamp:    timestamppb.New(e.Timestamp),
				Metadata:     meta,
				ErrorMessage: e.ErrorMessage,
			})
		}
	}
	return res, nil
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
	err        error
	sentEmails []gsmail.Email
}

func (m *mockSender) Send(ctx context.Context, email gsmail.Email) error {
	m.sentEmails = append(m.sentEmails, email)
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
				To:         []string{"to@example.com"},
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
				To:         []string{"to@example.com"},
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
		{
			name: "Domain Mismatch",
			provider: &providerEntities.EmailProvider{
				ID:   "p1",
				Name: "SMTP Provider",
				Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
				Config: func() []byte {
					c := &panmailv1.SmtpConfig{Host: "smtp.example.com"}
					b, _ := json.Marshal(c)
					return b
				}(),
			},
			sender: &mockSender{},
			req: &panmailv1.SendEmailRequest{
				ProviderId: "p1",
				From:       "user@wrongdomain.com",
				To:         []string{"to@example.com"},
				Subject:    "Hello",
				Body:       "World",
			},
			wantErr: true,
		},
		{
			name: "Domain Match",
			provider: &providerEntities.EmailProvider{
				ID:   "p1",
				Name: "SMTP Provider",
				Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
				Config: func() []byte {
					c := &panmailv1.SmtpConfig{Host: "smtp.example.com"}
					b, _ := json.Marshal(c)
					return b
				}(),
			},
			sender: &mockSender{},
			req: &panmailv1.SendEmailRequest{
				ProviderId: "p1",
				From:       "user@example.com",
				To:         []string{"to@example.com"},
				Subject:    "Hello",
				Body:       "World",
			},
			wantErr: false,
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

func TestSendEmailUsecase_doSend_MultiRecipient(t *testing.T) {
	provider := &providerEntities.EmailProvider{
		ID: "p1", Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
	}
	repo := &mockProviderRepo{provider: provider}
	sender := &mockSender{}
	eventUsecase := &mockEventUsecase{}
	factory := &mockFactory{sender: sender}
	u := NewSendEmailUsecase(repo, nil, nil, nil, eventUsecase, factory, NewTemplateRenderer(), "http://localhost")

	req := &panmailv1.SendEmailRequest{
		ProviderId: "p1",
		From:       "from@example.com",
		To:         []string{"a@example.com", "b@example.com"},
		Subject:    "Hello",
		BodyHtml:   "<html><body><a href=\"https://google.com\">Click me</a></body></html>",
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	_, err := u.SendEmail(ctx, "test-tenant", req)
	if err != nil {
		t.Fatalf("doSend failed: %v", err)
	}

	// Verify we sent 2 separate emails
	if len(sender.sentEmails) != 2 {
		t.Errorf("expected 2 sent emails, got %d", len(sender.sentEmails))
	}

	// Verify events: each recipient should have SENT and DELIVERED
	sentA, deliveredA, sentB, deliveredB := false, false, false, false
	for _, e := range eventUsecase.events {
		if e.Recipient == "a@example.com" {
			if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT {
				sentA = true
			}
			if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED {
				deliveredA = true
			}
		}
		if e.Recipient == "b@example.com" {
			if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT {
				sentB = true
			}
			if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED {
				deliveredB = true
			}
		}
	}

	if !sentA || !deliveredA || !sentB || !deliveredB {
		t.Errorf("missing events for recipients. A: sent=%v, deliv=%v; B: sent=%v, deliv=%v", sentA, deliveredA, sentB, deliveredB)
	}

	// Verify tracking URLs are unique per recipient
	urlA := ""
	urlB := ""
	for _, email := range sender.sentEmails {
		body := string(email.HTMLBody)
		if strings.Contains(string(email.To[0]), "a@example.com") {
			urlA = body
		} else if strings.Contains(string(email.To[0]), "b@example.com") {
			urlB = body
		}
	}

	if urlA == urlB {
		t.Error("tracking URLs should be unique for each recipient")
	}

	recipientAEncoded := base64.RawURLEncoding.EncodeToString([]byte("a@example.com"))
	recipientBEncoded := base64.RawURLEncoding.EncodeToString([]byte("b@example.com"))

	if !strings.Contains(urlA, recipientAEncoded) {
		t.Errorf("urlA missing recipient encoding: %s", urlA)
	}
	if !strings.Contains(urlB, recipientBEncoded) {
		t.Errorf("urlB missing recipient encoding: %s", urlB)
	}
}

func TestSendEmailUsecase_MultiRecipient_PartialFailure(t *testing.T) {
	provider := &providerEntities.EmailProvider{
		ID: "p1", Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
	}
	repo := &mockProviderRepo{provider: provider}

	// Sender that fails for a specific recipient
	sender := &mockSenderFunc{
		sendFn: func(email gsmail.Email) error {
			if email.To[0] == "fail@example.com" {
				return errors.New("delivery failed")
			}
			return nil
		},
	}

	eventUsecase := &mockEventUsecase{}
	factory := &mockFactory{sender: sender}
	u := NewSendEmailUsecase(repo, nil, nil, nil, eventUsecase, factory, NewTemplateRenderer(), "http://localhost")

	req := &panmailv1.SendEmailRequest{
		ProviderId: "p1",
		From:       "from@example.com",
		To:         []string{"success@example.com", "fail@example.com", "another-success@example.com"},
		Subject:    "Partial Failure Test",
		Body:       "Hello",
	}

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	_, err := u.SendEmail(ctx, "test-tenant", req)

	// Should NOT return error because some succeeded
	if err != nil {
		t.Fatalf("SendEmail failed but should have continued: %v", err)
	}

	// Verify events
	delivered := make(map[string]bool)
	deferred := make(map[string]bool)
	for _, e := range eventUsecase.events {
		if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED {
			delivered[e.Recipient] = true
		}
		if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DEFERRED {
			deferred[e.Recipient] = true
		}
	}

	if !delivered["success@example.com"] {
		t.Error("success@example.com should be delivered")
	}
	if !delivered["another-success@example.com"] {
		t.Error("another-success@example.com should be delivered")
	}
	if delivered["fail@example.com"] {
		t.Error("fail@example.com should NOT be delivered")
	}
	if !deferred["fail@example.com"] {
		t.Error("fail@example.com should be deferred")
	}
}

type mockSenderFunc struct {
	sendFn func(email gsmail.Email) error
}

func (m *mockSenderFunc) Send(ctx context.Context, email gsmail.Email) error {
	return m.sendFn(email)
}
func (m *mockSenderFunc) Validate(ctx context.Context, email string) error { return nil }
func (m *mockSenderFunc) Ping(ctx context.Context) error                   { return nil }
func (m *mockSenderFunc) SetRetryConfig(config gsmail.RetryConfig)         {}
