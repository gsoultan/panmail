// This ran behind a `sdkcontract` build tag until 2026-09-04, because
// github.com/gsoultan/panmail-sdk had never been tagged and so could only be
// reached through a go.work pointing at a local checkout. The condition its own
// comment set — "once panmail-sdk is pushed and tagged, drop the tag" — was met
// when the SDK was tagged v0.1.0-rc.1.
//
// It is a plain test now, which is the whole point: behind the tag it ran when
// somebody remembered to run it, which is a guard against proto drift that does
// not guard anything. The SDK speaks Connect JSON by hand against copies of
// these protos, so nothing except this test compares the two.
//
// The direction of the dependency is still worth understanding. A private
// gateway must not be imported by its public client; the reverse is fine, and
// is what this is — a test dependency on a published module, in go.mod like any
// other. If the SDK ever needs to import panmail, that is the thing to refuse.

package sdkcontract_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	panmail "github.com/gsoultan/panmail-sdk"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	authentities "github.com/gsoultan/panmail/internal/auth/entities"
	authmiddlewares "github.com/gsoultan/panmail/internal/auth/middlewares"
	authusecases "github.com/gsoultan/panmail/internal/auth/usecases"
)

/*
The published SDK against the gateway's real authentication.

The SDK's own tests prove it speaks Connect JSON. This proves it speaks it to
*panmail*: that a key sent in X-API-Key reaches the middleware, that the tenant
comes off the key, and that the email:send scope is what the RBAC interceptor
actually demands.

It matters more than it did when the client lived in this repo. The SDK no
longer shares a single generated type with the gateway — it builds the JSON by
hand — so this is now the only thing standing between a field rename in the
proto and every external application failing at once, with the SDK and the
gateway each looking correct on its own.

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

// sendRecorder stands in for the send usecase and records both the tenant the
// gateway resolved and the request it decoded — the second is what catches the
// SDK and the proto disagreeing about a field name.
type sendRecorder struct {
	panmailv1connect.UnimplementedEmailServiceHandler
	tenantID string
	req      *panmailv1.SendEmailRequest
}

func (r *sendRecorder) SendEmail(
	ctx context.Context,
	req *connect.Request[panmailv1.SendEmailRequest],
) (*connect.Response[panmailv1.SendEmailResponse], error) {
	r.tenantID = authmiddlewares.GetTenantID(ctx)
	r.req = req.Msg
	return connect.NewResponse(&panmailv1.SendEmailResponse{
		MessageId: "msg-1",
		Status:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING,
	}), nil
}

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
		Cc:         []string{"cc@example.org"},
		Bcc:        []string{"bcc@example.org"},
		Subject:    "Your receipt",
		HTML:       "<p>Thanks.</p>",
		Text:       "Thanks.",
		Attachments: []panmail.Attachment{{
			Filename: "receipt.pdf",
			Content:  []byte("%PDF-1.4"),
		}},
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
	if result.Status != panmail.StatusPending {
		t.Errorf("status = %q; want %q", result.Status, panmail.StatusPending)
	}
	// The tenant is never sent by the client. It comes off the key, which is
	// what stops one tenant's key from sending as another.
	if recorder.tenantID != "tenant-a" {
		t.Errorf("tenant = %q; want it taken from the api key", recorder.tenantID)
	}
}

// Every field the SDK writes by hand, read back through the generated type the
// gateway actually uses. A camelCase name the SDK invented would arrive empty.
func TestTheGatewayDecodesEveryFieldTheSDKSends(t *testing.T) {
	recorder, baseURL := realGateway(t, &keyStore{
		key: &authentities.ApiKey{
			TenantID: "tenant-a",
			Scopes:   []authentities.Scope{authentities.ScopeEmailSend},
		},
	})

	client, _ := panmail.New(baseURL, "pk_live_whatever")
	if _, err := client.Send(t.Context(), message()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := recorder.req
	if got == nil {
		t.Fatal("the handler decoded no request")
	}

	for name, check := range map[string]struct{ got, want string }{
		"provider_id": {got.ProviderId, "0f8b8f4e-0000-4000-8000-000000000000"},
		"from":        {got.From, "noreply@example.com"},
		"subject":     {got.Subject, "Your receipt"},
		"body_html":   {got.BodyHtml, "<p>Thanks.</p>"},
		"body_text":   {got.BodyText, "Thanks."},
	} {
		if check.got != check.want {
			t.Errorf("%s = %q; want %q", name, check.got, check.want)
		}
	}

	for name, list := range map[string][]string{
		"to":  got.To,
		"cc":  got.Cc,
		"bcc": got.Bcc,
	} {
		if len(list) != 1 {
			t.Errorf("%s = %v; want one address", name, list)
		}
	}

	// bytes is base64 in protobuf JSON. Raw bytes would decode to mojibake
	// here rather than fail, which is exactly why it is asserted.
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %v; want one", got.Attachments)
	}
	if string(got.Attachments[0].Content) != "%PDF-1.4" {
		t.Errorf("attachment content = %q; want %%PDF-1.4", got.Attachments[0].Content)
	}
	if got.Attachments[0].ContentType != "application/pdf" {
		t.Errorf("attachment content type = %q; want application/pdf", got.Attachments[0].ContentType)
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
