package usecases

import (
	"context"
	"errors"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/internal/emailfilter"
	suppressionentities "github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// suppressesOne suppresses exactly one address, so a refusal can land after
// other recipients have been looked at.
type suppressesOne struct {
	mockSuppressionRepo
	address string
}

func (s *suppressesOne) GetByEmails(_ context.Context, _ string, emails []string) (map[string]*suppressionentities.Suppression, error) {
	found := map[string]*suppressionentities.Suppression{}
	for _, e := range emails {
		if e == s.address {
			found[e] = &suppressionentities.Suppression{Email: e, Reason: "unsubscribed"}
		}
	}
	return found, nil
}

// outboxThatRefuses fails every write, as a database that is down does.
type outboxThatRefuses struct{ mockOutboxRepo }

func (o *outboxThatRefuses) Create(context.Context, *entities.OutboxEmail) error {
	return errors.New("database is down")
}

// A refused message must leave no PENDING behind. PENDING was recorded per
// recipient as each one passed the suppression check -- before the rest were
// checked, before the template was rendered, before the filter ran, before the
// outbox write -- so every refusal after the first recipient left PENDING
// events for a message the caller was told was refused. Nothing ever moves
// those on: the tenant's history showed them queued for good.
func TestARefusedMessageLeavesNoPendingEvents(t *testing.T) {
	provider := &providerEntities.EmailProvider{
		ID: testProviderID, TenantID: testTenantID, Name: "test", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
	}
	request := func() *panmailv1.SendEmailRequest {
		return &panmailv1.SendEmailRequest{
			ProviderId: testProviderID, From: "from@example.com",
			To: []string{"first@example.com", "second@example.com"}, Subject: "Hi", BodyText: "hi",
		}
	}

	testCases := []struct {
		name     string
		deps     func(*SendEmailDeps)
		req      func() *panmailv1.SendEmailRequest
		wantType panmailv1.EmailEventType // the refusal's own record, if any
	}{
		{
			name:     "the second recipient is suppressed",
			deps:     func(d *SendEmailDeps) { d.SuppressionRepo = &suppressesOne{address: "second@example.com"} },
			req:      request,
			wantType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DROPPED,
		},
		{
			name: "the template does not exist",
			deps: func(*SendEmailDeps) {},
			req: func() *panmailv1.SendEmailRequest {
				r := request()
				r.TemplateId = "00000000-0000-0000-0000-00000000dead"
				return r
			},
		},
		{
			name: "a filter rule rejects it",
			deps: func(d *SendEmailDeps) {
				d.Screener = &fakeScreener{decision: decisionOf(emailfilter.ActionReject, "no")}
			},
			req:      request,
			wantType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED,
		},
		{
			name: "the outbox write fails",
			deps: func(d *SendEmailDeps) { d.OutboxRepo = &outboxThatRefuses{} },
			req:  request,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			events := &mockEventUsecase{}
			deps := SendEmailDeps{
				ProviderRepo:    &mockProviderRepo{provider: provider},
				TemplateRepo:    &mockTemplateRepo{},
				SuppressionRepo: &mockSuppressionRepo{},
				OutboxRepo:      &mockOutboxRepo{},
				EventUsecase:    events,
				ProviderFactory: &mockFactory{sender: &mockSender{}},
				Renderer:        NewTemplateRenderer(),
				BaseURL:         "http://localhost",
				TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
			}
			tc.deps(&deps)

			if _, err := NewSendEmailUsecase(deps).SendEmail(context.Background(), testTenantID, tc.req()); err == nil {
				t.Fatal("SendEmail succeeded; the case is meant to be refused")
			}

			for _, e := range events.events {
				if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING {
					t.Errorf("a refused message left PENDING for %s", e.Recipient)
				}
			}
			if tc.wantType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
				found := false
				for _, e := range events.events {
					found = found || e.Type == tc.wantType
				}
				if !found {
					t.Errorf("the refusal's own %v was not recorded", tc.wantType)
				}
			}
		})
	}
}

// And an accepted message still records PENDING for every recipient.
func TestAnAcceptedMessageIsPendingForEveryRecipient(t *testing.T) {
	events := &mockEventUsecase{}
	u := NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo: &mockProviderRepo{provider: &providerEntities.EmailProvider{
			ID: testProviderID, TenantID: testTenantID, Name: "test", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		}},
		TemplateRepo: &mockTemplateRepo{}, SuppressionRepo: &mockSuppressionRepo{},
		OutboxRepo: &mockOutboxRepo{}, EventUsecase: events,
		ProviderFactory: &mockFactory{sender: &mockSender{}}, Renderer: NewTemplateRenderer(),
	})
	to := []string{"a@example.com", "b@example.com", "c@example.com"}
	if _, err := u.SendEmail(context.Background(), testTenantID, &panmailv1.SendEmailRequest{
		ProviderId: testProviderID, From: "from@example.com", To: to, Subject: "Hi", BodyText: "hi",
	}); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	pending := map[string]bool{}
	for _, e := range events.events {
		if e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING {
			pending[e.Recipient] = true
		}
	}
	for _, r := range to {
		if !pending[r] {
			t.Errorf("%s has no PENDING", r)
		}
	}
}
