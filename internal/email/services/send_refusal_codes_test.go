package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/usecases"
)

// refusingUsecase answers every send with one prepared error.
type refusingUsecase struct {
	err error
}

func (u *refusingUsecase) SendEmail(context.Context, string, *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error) {
	return nil, u.err
}

func (u *refusingUsecase) RecordEvent(context.Context, string, string, string, panmailv1.EmailEventType, string, string, string, map[string]any) error {
	return nil
}

func (u *refusingUsecase) RegisterQueueWorker(usecases.QueueWorker) {}

func send(t *testing.T, err error) *connect.Error {
	t.Helper()

	svc := NewEmailService(&refusingUsecase{err: err})
	_, gotErr := svc.SendEmail(context.Background(),
		connect.NewRequest(&panmailv1.SendEmailRequest{}))
	if gotErr == nil {
		t.Fatal("SendEmail() returned no error")
	}

	var connectErr *connect.Error
	if !errors.As(gotErr, &connectErr) {
		t.Fatalf("error is %T, not a *connect.Error — it would reach a client as unknown/500", gotErr)
	}
	return connectErr
}

// A bare handler error renders as CodeUnknown, which a client sees as HTTP 500:
// "the gateway broke, try again later". These refusals are the opposite — the
// same request is refused identically until somebody changes something — so a
// caller that backs off and retries burns attempts on an answer that cannot
// change.
func TestRefusalsThatAreDecisionsGetTheirOwnCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want connect.Code
	}{
		{
			// FailedPrecondition, not InvalidArgument: the request is well
			// formed and the caller is allowed to make it, but the system
			// cannot succeed until the suppression is lifted.
			name: "suppressed recipient",
			err:  &usecases.SuppressedRecipientError{Recipient: "gone@example.com", Reason: "hard bounce"},
			want: connect.CodeFailedPrecondition,
		},
		{
			// Same reasoning as suppression: a well-formed request the caller
			// may make, refused by configuration that will not change on its
			// own.
			name: "provider not authorized for the sender's domain",
			err:  &usecases.ProviderDomainRefusedError{Provider: "ESP", Domain: "example.com"},
			want: connect.CodeFailedPrecondition,
		},
		{
			name: "provider does not exist",
			err:  &usecases.ProviderNotFoundError{ProviderID: "nope"},
			want: connect.CodeInvalidArgument,
		},
		{
			name: "template does not exist",
			err:  &usecases.TemplateRefusedError{TemplateID: "nope"},
			want: connect.CodeInvalidArgument,
		},
		{
			name: "template will not render",
			err: &usecases.TemplateRefusedError{
				TemplateID: "t1", Stage: "subject", Err: errors.New("unclosed {{"),
			},
			want: connect.CodeInvalidArgument,
		},
		{
			// The capacity refusals keep the code they already had.
			name: "backlog full",
			err:  &usecases.BacklogFullError{},
			want: connect.CodeResourceExhausted,
		},
		{
			name: "rate limited",
			err:  &usecases.RateLimitedError{TenantID: "t", RetryAfter: 2 * time.Second},
			want: connect.CodeResourceExhausted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := send(t, tt.err).Code(); got != tt.want {
				t.Fatalf("code = %v, want %v", got, tt.want)
			}
		})
	}
}

// Anything that is not a known refusal stays unknown on purpose: a storage
// failure may well succeed on a retry, and telling a caller otherwise is
// worse than telling them nothing.
func TestAnUnrecognisedFailureStaysUnknown(t *testing.T) {
	svc := NewEmailService(&refusingUsecase{err: errors.New("database is on fire")})

	_, err := svc.SendEmail(context.Background(), connect.NewRequest(&panmailv1.SendEmailRequest{}))
	if err == nil {
		t.Fatal("SendEmail() returned no error")
	}
	if connect.CodeOf(err) != connect.CodeUnknown {
		t.Fatalf("code = %v, want unknown", connect.CodeOf(err))
	}
}

// The refusal has to survive being wrapped on its way up, or the mapping
// silently stops applying the first time a call site adds context.
func TestARefusalIsRecognisedThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("sending failed: %w",
		&usecases.SuppressedRecipientError{Recipient: "gone@example.com", Reason: "hard bounce"})

	if got := send(t, wrapped).Code(); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want failed_precondition", got)
	}
}

// The outbox worker classifies a failed send from e.LastError -- the string,
// not the type -- so rewording one of these changes how a retry is classified.
// These are the messages the bare fmt.Errorf calls produced.
func TestRefusalMessagesAreUnchanged(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{
			&usecases.SuppressedRecipientError{Recipient: "a@example.com", Reason: "hard bounce"},
			"recipient a@example.com is suppressed: hard bounce",
		},
		{
			&usecases.ProviderNotFoundError{ProviderID: "p1"},
			"provider not found: p1",
		},
		{
			&usecases.TemplateRefusedError{TemplateID: "t1"},
			"template not found: t1",
		},
		{
			&usecases.TemplateRefusedError{TemplateID: "t1", Stage: "body_html", Err: errors.New("boom")},
			"failed to render body_html: boom",
		},
	}

	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}

// A caller that wants the underlying render failure should be able to reach it.
func TestTemplateRefusalUnwraps(t *testing.T) {
	inner := errors.New("unclosed {{")
	err := &usecases.TemplateRefusedError{TemplateID: "t1", Stage: "subject", Err: inner}

	if !errors.Is(err, inner) {
		t.Fatal("the render failure is not reachable through the refusal")
	}
}
