package sdkcontract_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	panmail "github.com/gsoultan/panmail-sdk"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	authentities "github.com/gsoultan/panmail/internal/auth/entities"
	authmiddlewares "github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/email/services"
	"github.com/gsoultan/panmail/internal/email/usecases"
)

/*
Every send refusal the gateway can make, from the error the usecase returns to
the type the SDK hands its caller.

The rest of this package stands in for the email service, which is right for
proving the auth stack but skips the one piece of the gateway that decides what
a refusal looks like on the wire: the handler's mapping from usecase errors to
Connect codes. That mapping and the SDK's classification are two halves of one
contract written in two repositories, and until now nothing held them together.
Each has its own tests, and each agrees with itself.

So this drives the real email service handler, behind the real auth middleware
and RBAC interceptor, with only the usecase stubbed. If the handler changes the
code it sends for a refusal, or the SDK changes how it reads one, a case here
fails — which is the whole reason it is in this repository rather than either
side testing its own half.
*/

// refusingUsecase is a send usecase that refuses with whatever it is told to.
// Everything past SendEmail is unused by the handler under test.
type refusingUsecase struct {
	err error
}

func (u refusingUsecase) SendEmail(context.Context, string, *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error) {
	return nil, u.err
}

func (refusingUsecase) RecordEvent(context.Context, string, string, string, panmailv1.EmailEventType, string, string, string, map[string]any) error {
	return nil
}

func (refusingUsecase) RegisterQueueWorker(usecases.QueueWorker) {}

// refusingGateway serves the real email service over the real auth stack,
// refusing every send with err.
func refusingGateway(t *testing.T, err error) string {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(panmailv1connect.NewEmailServiceHandler(
		services.NewEmailService(refusingUsecase{err: err}),
		connect.WithInterceptors(authmiddlewares.NewRBACInterceptor()),
	))

	keys := &keyStore{key: &authentities.ApiKey{
		TenantID: "tenant-a",
		Scopes:   []authentities.Scope{authentities.ScopeEmailSend},
	}}
	server := httptest.NewServer(authmiddlewares.NewAuthMiddleware(nil, keys).Handle(mux))
	t.Cleanup(server.Close)

	return server.URL
}

func sendAgainst(t *testing.T, baseURL string) error {
	t.Helper()

	client, err := panmail.New(baseURL, "pk_live_whatever")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Send(t.Context(), message())
	if err == nil {
		t.Fatal("the send was accepted; the gateway was told to refuse it")
	}
	return err
}

func TestASuppressedRecipientReachesTheCallerAsItsOwnRefusal(t *testing.T) {
	err := sendAgainst(t, refusingGateway(t, &usecases.SuppressedRecipientError{
		Recipient: "bounced@example.net",
		Reason:    "hard bounce",
	}))

	var suppressed *panmail.SuppressedRecipientError
	if !errors.As(err, &suppressed) {
		t.Fatalf("the SDK did not surface a SuppressedRecipientError, got %T: %v", err, err)
	}

	// A suppression refuses the whole message and never succeeds on retry, so
	// the one thing it must not look like is either capacity refusal.
	if errors.As(err, new(*panmail.RateLimitedError)) || errors.As(err, new(*panmail.BacklogFullError)) {
		t.Errorf("a suppressed recipient also read as a capacity refusal: %v", err)
	}
}

func TestARateLimitReachesTheCallerWithItsDelay(t *testing.T) {
	err := sendAgainst(t, refusingGateway(t, &usecases.RateLimitedError{
		TenantID:   "tenant-a",
		RetryAfter: 3 * time.Second,
	}))

	var limited *panmail.RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("the SDK did not surface a RateLimitedError, got %T: %v", err, err)
	}
	if limited.RetryAfter != 3*time.Second {
		t.Errorf("RetryAfter = %v; want 3s — the handler's Retry-After did not survive the trip", limited.RetryAfter)
	}
}

// The same Connect code as a rate limit, told apart only by the Retry-After
// the handler leaves off. Removing it from the rate refusal would silently turn
// every rate limit into this, so it is worth holding from both ends.
func TestAFullBacklogReachesTheCallerAsDistinctFromARateLimit(t *testing.T) {
	err := sendAgainst(t, refusingGateway(t, &usecases.BacklogFullError{
		TenantID: "tenant-a",
		Pending:  5000,
		Ceiling:  1000,
	}))

	var full *panmail.BacklogFullError
	if !errors.As(err, &full) {
		t.Fatalf("the SDK did not surface a BacklogFullError, got %T: %v", err, err)
	}
	if errors.As(err, new(*panmail.RateLimitedError)) {
		t.Error("a full backlog read as a rate limit, which a caller would put on a timer")
	}
}

// An unusable request is a refusal but not one the SDK gives its own type:
// there is nothing to branch on beyond "fix the request". It has to arrive as
// invalid_argument, and it must not be mistaken for a suppression, which is
// also a 400.
func TestAnUnusableRequestReachesTheCallerAsInvalidArgument(t *testing.T) {
	for name, refusal := range map[string]error{
		"provider not found": &usecases.ProviderNotFoundError{ProviderID: "0f8b"},
		"template refused":   &usecases.TemplateRefusedError{TemplateID: "receipt", Stage: "subject", Err: errors.New("unknown field")},
	} {
		t.Run(name, func(t *testing.T) {
			err := sendAgainst(t, refusingGateway(t, refusal))

			var apiErr *panmail.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("the SDK did not surface an APIError, got %T: %v", err, err)
			}
			if apiErr.Code != "invalid_argument" {
				t.Errorf("Code = %q; want invalid_argument", apiErr.Code)
			}
			if errors.As(err, new(*panmail.SuppressedRecipientError)) {
				t.Error("an unusable request was read as a suppressed recipient")
			}
		})
	}
}

// The one answer that means "try again": a failure rather than a refusal. It
// has to stay distinguishable from every refusal above, or a caller cannot
// tell which of its errors a backoff could fix.
func TestAFailureReachesTheCallerAsUnknown(t *testing.T) {
	err := sendAgainst(t, refusingGateway(t, errors.New("storage unavailable")))

	var apiErr *panmail.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("the SDK did not surface an APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "unknown" {
		t.Errorf("Code = %q; want unknown", apiErr.Code)
	}
	for name, is := range map[string]bool{
		"SuppressedRecipientError": errors.As(err, new(*panmail.SuppressedRecipientError)),
		"RateLimitedError":         errors.As(err, new(*panmail.RateLimitedError)),
		"BacklogFullError":         errors.As(err, new(*panmail.BacklogFullError)),
	} {
		if is {
			t.Errorf("a failure was read as a %s", name)
		}
	}
}
