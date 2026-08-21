package panmail_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	authentities "github.com/gsoultan/panmail/internal/auth/entities"
	authmiddlewares "github.com/gsoultan/panmail/internal/auth/middlewares"
	authusecases "github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/pkg/panmail"
)

/*
The SDK against the gateway's real authentication.

client_test.go proves the client speaks Connect. This proves it speaks it to
*panmail*: that a key sent in X-API-Key reaches the middleware, that the tenant
comes off the key, and that the email:send scope is what the RBAC interceptor
actually demands. Those three are a contract between two packages that never
import each other, so nothing else would notice them drifting apart — the
symptom would be every send from every external application failing at once,
with the SDK and the gateway each looking correct on its own.

Only the key lookup is stubbed. The middleware, the interceptor and the policy
table are the ones the binary runs.
*/

type keyStore struct {
	key *authentities.ApiKey
	err error
}

func (s *keyStore) VerifyApiKey(context.Context, string) (*authentities.ApiKey, error) {
	return s.key, s.err
}

func (s *keyStore) CreateApiKey(context.Context, authusecases.NewApiKey) (*authentities.ApiKey, string, error) {
	return nil, "", nil
}

func (s *keyStore) ListApiKeys(context.Context, string, int, string) ([]*authentities.ApiKey, string, error) {
	return nil, "", nil
}

func (s *keyStore) DeleteApiKey(context.Context, string, string) error  { return nil }
func (s *keyStore) DisableApiKey(context.Context, string, string) error { return nil }
func (s *keyStore) EnableApiKey(context.Context, string, string) error  { return nil }

// sendRecorder stands in for the send usecase and records the tenant the
// gateway resolved, which is the whole point of authenticating with a key.
type sendRecorder struct {
	panmailv1connect.UnimplementedEmailServiceHandler
	tenantID string
}

func (r *sendRecorder) SendEmail(
	ctx context.Context,
	_ *connect.Request[panmailv1.SendEmailRequest],
) (*connect.Response[panmailv1.SendEmailResponse], error) {
	r.tenantID = authmiddlewares.GetTenantID(ctx)
	return connect.NewResponse(&panmailv1.SendEmailResponse{
		MessageId: "msg-1",
		Status:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING,
	}), nil
}

// realGateway serves the email service behind the same middleware and
// interceptor cmd/api wires up.
func realGateway(t *testing.T, keys *keyStore) (*sendRecorder, string) {
	t.Helper()

	recorder := &sendRecorder{}
	mux := http.NewServeMux()
	mux.Handle(panmailv1connect.NewEmailServiceHandler(
		recorder,
		connect.WithInterceptors(authmiddlewares.NewRBACInterceptor()),
	))

	middleware := authmiddlewares.NewAuthMiddleware(nil, keys)
	server := httptest.NewServer(middleware.Handle(mux))
	t.Cleanup(server.Close)

	return recorder, server.URL
}

func message() panmail.Message {
	return panmail.Message{
		ProviderID: "0f8b8f4e-0000-4000-8000-000000000000",
		From:       "noreply@example.com",
		To:         []string{"someone@example.org"},
		Subject:    "Your receipt",
		HTML:       "<p>Thanks.</p>",
	}
}

func TestSendThroughTheRealAuthStack(t *testing.T) {
	recorder, baseURL := realGateway(t, &keyStore{
		key: &authentities.ApiKey{
			TenantID: "tenant-a",
			Scopes:   []authentities.Scope{authentities.ScopeEmailSend},
		},
	})

	client, err := panmail.New(baseURL, "pk_live_whatever")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := client.Send(t.Context(), message())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.MessageID != "msg-1" {
		t.Errorf("message id = %q; want msg-1", result.MessageID)
	}
	// The tenant is never sent by the client. It comes off the key, which is
	// what stops one tenant's key from sending as another.
	if recorder.tenantID != "tenant-a" {
		t.Errorf("tenant = %q; want it taken from the api key", recorder.tenantID)
	}
}

func TestSendIsRefusedWithoutTheSendScope(t *testing.T) {
	recorder, baseURL := realGateway(t, &keyStore{
		key: &authentities.ApiKey{
			TenantID: "tenant-a",
			Scopes:   []authentities.Scope{authentities.ScopeEventsRead},
		},
	})

	client, err := panmail.New(baseURL, "pk_live_readonly")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Send(t.Context(), message())
	if err == nil {
		t.Fatal("a read-only key was allowed to send")
	}

	// Surfaced as something the caller can act on rather than a bare transport
	// error: the fix is to add a scope to the key, and the error has to say so.
	var authErr *panmail.AuthError
	if !errors.As(err, &authErr) {
		t.Errorf("error = %v; want an AuthError", err)
	}
	if recorder.tenantID != "" {
		t.Error("the send reached the handler despite the key lacking the scope")
	}
}

func TestSendIsRefusedWithAnUnknownKey(t *testing.T) {
	_, baseURL := realGateway(t, &keyStore{err: errors.New("no such key")})

	client, err := panmail.New(baseURL, "pk_live_revoked")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Send(t.Context(), message())
	if err == nil {
		t.Fatal("a revoked key was allowed to send")
	}
	var authErr *panmail.AuthError
	if !errors.As(err, &authErr) {
		t.Errorf("error = %v; want an AuthError", err)
	}
}
