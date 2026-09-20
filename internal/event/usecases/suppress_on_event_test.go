package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerentities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	evententities "github.com/gsoultan/panmail/internal/event/repositories/entities"
)

type suppressionCall struct {
	tenantID string
	email    string
	reason   string
}

type fakeSuppressor struct {
	calls []suppressionCall
	err   error
}

func (f *fakeSuppressor) Add(
	_ context.Context, tenantID string, req *panmailv1.AddSuppressionRequest,
) (*panmailv1.Suppression, error) {
	f.calls = append(f.calls, suppressionCall{tenantID, req.Email, req.Reason})
	if f.err != nil {
		return nil, f.err
	}
	return &panmailv1.Suppression{Email: req.Email}, nil
}

func newSuppressionTestUsecase(s Suppressor) ProcessEventUsecase {
	uc := NewProcessEventUsecase(
		&mockEventRepo{messages: make(map[string]*evententities.EmailMessage)},
		&mockInboundRepo{},
		&mockOutboxRepo{},
		&mockProviderRepo{providers: make(map[string]*providerentities.EmailProvider)},
		nil,
	)
	if s != nil {
		uc.SetSuppressor(s)
	}
	return uc
}

// The exclusions matter more than the inclusions. A soft bounce is a full
// mailbox or a busy server, and suppressing on one destroys a tenant's ability
// to reach a customer who is still reachable.
func TestSuppressionOnlyFollowsTerminalEvents(t *testing.T) {
	tests := []struct {
		name         string
		eventType    panmailv1.EmailEventType
		errorMessage string
		wantSuppress bool
	}{
		{
			name:         "hard bounce suppresses",
			eventType:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
			wantSuppress: true,
		},
		{
			name:         "spam complaint suppresses",
			eventType:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT,
			wantSuppress: true,
		},
		{
			name:         "soft bounce does not",
			eventType:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			wantSuppress: false,
		},
		{
			name:         "deferred does not",
			eventType:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DEFERRED,
			wantSuppress: false,
		},
		{
			name:         "delivered does not",
			eventType:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
			wantSuppress: false,
		},
		{
			name:         "opened does not",
			eventType:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED,
			wantSuppress: false,
		},
		{
			// The unsubscribe handler suppresses with its own wording before
			// it records the event. Doing it here too would make every
			// one-click unsubscribe a duplicate insert.
			name:         "unsubscribe is left to the unsubscribe handler",
			eventType:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED,
			wantSuppress: false,
		},
		{
			// ClassifyError leaves an error it does not recognise as BOUNCED
			// and retryable rather than condemning the address.
			name:         "an unrecognised generic bounce does not",
			eventType:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED,
			errorMessage: "something went wrong",
			wantSuppress: false,
		},
		{
			// ...but a generic bounce that classifies as hard does, which is
			// how SendGrid and Mailgun reach the suppression list at all.
			name:         "a generic bounce that classifies as hard does",
			eventType:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED,
			errorMessage: "550 5.1.1 user unknown",
			wantSuppress: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sup := &fakeSuppressor{}
			uc := newSuppressionTestUsecase(sup)

			err := uc.RecordEvent(context.Background(), "tenant-1", "prov-1", "msg-1",
				tt.eventType, "user@example.com", "", tt.errorMessage, nil)
			if err != nil {
				t.Fatalf("RecordEvent() error = %v", err)
			}

			if got := len(sup.calls) > 0; got != tt.wantSuppress {
				t.Fatalf("suppressed = %v, want %v (calls: %+v)", got, tt.wantSuppress, sup.calls)
			}
			if tt.wantSuppress {
				if sup.calls[0].tenantID != "tenant-1" {
					t.Errorf("suppressed for tenant %q, want tenant-1", sup.calls[0].tenantID)
				}
				if sup.calls[0].email != "user@example.com" {
					t.Errorf("suppressed %q, want user@example.com", sup.calls[0].email)
				}
				if sup.calls[0].reason == "" {
					t.Error("suppression recorded no reason")
				}
			}
		})
	}
}

// A hard bounce for one tenant must not suppress the address for another.
func TestSuppressionIsScopedToTheTenant(t *testing.T) {
	sup := &fakeSuppressor{}
	uc := newSuppressionTestUsecase(sup)

	if err := uc.RecordEvent(context.Background(), "tenant-b", "prov-1", "msg-1",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, "user@example.com", "", "550 unknown", nil); err != nil {
		t.Fatalf("RecordEvent() error = %v", err)
	}

	if len(sup.calls) != 1 || sup.calls[0].tenantID != "tenant-b" {
		t.Fatalf("calls = %+v, want one for tenant-b", sup.calls)
	}
}

// The diagnostic the provider sent is what tells an operator why an address
// they never suppressed by hand is on the list.
func TestSuppressionReasonCarriesTheDiagnostic(t *testing.T) {
	sup := &fakeSuppressor{}
	uc := newSuppressionTestUsecase(sup)

	if err := uc.RecordEvent(context.Background(), "tenant-1", "prov-1", "msg-1",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, "user@example.com", "",
		"550 5.1.1 no such mailbox", nil); err != nil {
		t.Fatalf("RecordEvent() error = %v", err)
	}

	if len(sup.calls) != 1 {
		t.Fatalf("calls = %+v, want one", sup.calls)
	}
	if !strings.Contains(sup.calls[0].reason, "550 5.1.1 no such mailbox") {
		t.Errorf("reason = %q, want it to carry the diagnostic", sup.calls[0].reason)
	}
	if !strings.Contains(sup.calls[0].reason, "hard bounce") {
		t.Errorf("reason = %q, want it to name the cause", sup.calls[0].reason)
	}
}

// Already suppressed is the ordinary case, not a failure: a provider retries a
// webhook, and the address ends up suppressed either way.
func TestRecordEventSurvivesADuplicateSuppression(t *testing.T) {
	sup := &fakeSuppressor{err: errors.New("UNIQUE constraint failed")}
	uc := newSuppressionTestUsecase(sup)

	err := uc.RecordEvent(context.Background(), "tenant-1", "prov-1", "msg-1",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, "user@example.com", "", "550 unknown", nil)
	if err != nil {
		t.Fatalf("RecordEvent() error = %v, want nil -- a duplicate suppression is not a failed event", err)
	}
	if len(sup.calls) != 1 {
		t.Fatalf("calls = %+v, want one attempt", sup.calls)
	}
}

// Wiring that never calls SetSuppressor must behave as it did before, not panic.
func TestRecordEventWithoutASuppressor(t *testing.T) {
	uc := newSuppressionTestUsecase(nil)

	if err := uc.RecordEvent(context.Background(), "tenant-1", "prov-1", "msg-1",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, "user@example.com", "", "550 unknown", nil); err != nil {
		t.Fatalf("RecordEvent() error = %v", err)
	}
}

// An event with no recipient has nothing to suppress; asking for an empty
// address would write a row that matches every lookup or none.
func TestNoSuppressionWithoutARecipient(t *testing.T) {
	sup := &fakeSuppressor{}
	uc := newSuppressionTestUsecase(sup)

	if err := uc.RecordEvent(context.Background(), "tenant-1", "prov-1", "",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, "", "", "550 unknown", nil); err != nil {
		t.Fatalf("RecordEvent() error = %v", err)
	}
	if len(sup.calls) != 0 {
		t.Fatalf("calls = %+v, want none", sup.calls)
	}
}
