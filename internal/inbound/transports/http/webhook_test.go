package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
)

const (
	tenantA   = "11111111-1111-1111-1111-111111111111"
	tenantB   = "22222222-2222-2222-2222-222222222222"
	providerA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	secretA   = "inbound-secret-a"
)

type stubUsecase struct {
	err      error
	received []*panmailv1.InboundEmail
}

func (s *stubUsecase) Process(_ context.Context, e *panmailv1.InboundEmail) error {
	s.received = append(s.received, e)
	return s.err
}
func (s *stubUsecase) List(context.Context, string, int, string) ([]*panmailv1.InboundEmail, string, error) {
	return nil, "", nil
}
func (s *stubUsecase) Get(context.Context, string, string) (*panmailv1.InboundEmail, error) {
	return nil, nil
}

// fakeProviders holds providers by tenant, so a lookup under the wrong tenant
// finds nothing -- as the real store's tenant-scoped query does.
type fakeProviders struct {
	providerStores.Repository
	byTenant map[string]map[string]*providerEntities.EmailProvider
}

func (f *fakeProviders) GetByID(_ context.Context, tenantID, id string) (*providerEntities.EmailProvider, error) {
	return f.byTenant[tenantID][id], nil
}

func providers(ps ...*providerEntities.EmailProvider) *fakeProviders {
	f := &fakeProviders{byTenant: map[string]map[string]*providerEntities.EmailProvider{}}
	for _, p := range ps {
		if f.byTenant[p.TenantID] == nil {
			f.byTenant[p.TenantID] = map[string]*providerEntities.EmailProvider{}
		}
		f.byTenant[p.TenantID][p.ID] = p
	}
	return f
}

func providerWith(typ panmailv1.ProviderType, secret string) *providerEntities.EmailProvider {
	return &providerEntities.EmailProvider{ID: providerA, TenantID: tenantA, Type: typ, WebhookSecret: secret}
}

func defaultProviders() *fakeProviders {
	return providers(providerWith(panmailv1.ProviderType_PROVIDER_TYPE_IMAP, secretA))
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func inboundPath(tenantID, providerID string) string {
	return "/inbound/" + tenantID + "/" + providerID
}

func serve(h *WebhookHandler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// signedJSON posts body to tenant A's provider, signed with its secret.
func signedJSON(t *testing.T, h *WebhookHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, inboundPath(tenantA, providerA), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, sign(secretA, []byte(body)))
	return serve(h, req)
}

// --- authentication ---

// The endpoint used to take the tenant from the body and check nothing. Any
// caller could put mail in any tenant's inbox, fire its MAIL_INBOUND webhooks,
// and with a bounce-shaped message suppress addresses permanently.
func TestAnUnauthenticatedPostIsRefused(t *testing.T) {
	body := `{"from":"a@b.c","to":["d@e.f"],"subject":"Undeliverable","text":"user unknown"}`

	cases := []struct {
		name      string
		path      string
		providers *fakeProviders
		prepare   func(*http.Request)
	}{
		{name: "no credential", path: inboundPath(tenantA, providerA), providers: defaultProviders(),
			prepare: func(*http.Request) {}},
		{name: "wrong signature", path: inboundPath(tenantA, providerA), providers: defaultProviders(),
			prepare: func(r *http.Request) { r.Header.Set(SignatureHeader, sign("guess", []byte(body))) }},
		{name: "signature not hex", path: inboundPath(tenantA, providerA), providers: defaultProviders(),
			prepare: func(r *http.Request) { r.Header.Set(SignatureHeader, "sha256=zz") }},
		{name: "wrong basic password", path: inboundPath(tenantA, providerA), providers: defaultProviders(),
			prepare: func(r *http.Request) { r.SetBasicAuth("", "guess") }},
		{name: "unknown provider", path: inboundPath(tenantA, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"), providers: defaultProviders(),
			prepare: func(r *http.Request) { r.Header.Set(SignatureHeader, sign(secretA, []byte(body))) }},
		// Tenant B's URL with tenant A's provider and secret: the credential
		// is scoped to the tenant that owns it.
		{name: "another tenant's provider", path: inboundPath(tenantB, providerA), providers: defaultProviders(),
			prepare: func(r *http.Request) { r.Header.Set(SignatureHeader, sign(secretA, []byte(body))) }},
		{name: "provider with no secret", path: inboundPath(tenantA, providerA),
			providers: providers(providerWith(panmailv1.ProviderType_PROVIDER_TYPE_IMAP, "")),
			prepare:   func(r *http.Request) { r.Header.Set(SignatureHeader, sign("", []byte(body))) }},
		// SendGrid's stored value is a public key and SES's a topic ARN.
		// Neither is a secret, so neither may serve as one.
		{name: "SendGrid public key as the secret", path: inboundPath(tenantA, providerA),
			providers: providers(providerWith(panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID, "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE")),
			prepare: func(r *http.Request) {
				r.Header.Set(SignatureHeader, sign("MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE", []byte(body)))
			}},
		{name: "SES topic ARN as the secret", path: inboundPath(tenantA, providerA),
			providers: providers(providerWith(panmailv1.ProviderType_PROVIDER_TYPE_SES, "arn:aws:sns:us-east-1:1:t")),
			prepare:   func(r *http.Request) { r.SetBasicAuth("", "arn:aws:sns:us-east-1:1:t") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usecase := &stubUsecase{}
			h := NewWebhookHandler(usecase, tc.providers)

			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			tc.prepare(req)
			rec := serve(h, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status %d, want 401", rec.Code)
			}
			if len(usecase.received) != 0 {
				t.Error("an unauthenticated post was processed")
			}
		})
	}
}

// The tenant comes from the credential. A tenant_id in the body -- which is
// what the endpoint used to trust -- changes nothing.
func TestTheTenantComesFromTheCredentialNotTheBody(t *testing.T) {
	usecase := &stubUsecase{}
	h := NewWebhookHandler(usecase, defaultProviders())

	rec := signedJSON(t, h, `{"tenant_id":"`+tenantB+`","from":"a@b.c","to":["d@e.f"],"subject":"hello"}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	if len(usecase.received) != 1 || usecase.received[0].TenantId != tenantA {
		t.Fatalf("processed %+v, want one message for tenant A", usecase.received)
	}
}

func TestABasicAuthenticatedPostIsAccepted(t *testing.T) {
	for _, tc := range []struct{ name, secret, user, pass string }{
		{name: "password only", secret: secretA, user: "", pass: secretA},
		{name: "user and password", secret: "hook:" + secretA, user: "hook", pass: secretA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usecase := &stubUsecase{}
			h := NewWebhookHandler(usecase, providers(providerWith(panmailv1.ProviderType_PROVIDER_TYPE_POSTMARK, tc.secret)))

			req := httptest.NewRequest(http.MethodPost, inboundPath(tenantA, providerA),
				bytes.NewBufferString(`{"from":"a@b.c","to":["d@e.f"],"subject":"hello"}`))
			req.Header.Set("Content-Type", "application/json")
			req.SetBasicAuth(tc.user, tc.pass)

			if rec := serve(h, req); rec.Code != http.StatusAccepted {
				t.Fatalf("status %d, want 202", rec.Code)
			}
			if len(usecase.received) != 1 || usecase.received[0].TenantId != tenantA {
				t.Errorf("processed %+v, want one message for tenant A", usecase.received)
			}
		})
	}
}

func TestTheOldBodyTenantURLIsGone(t *testing.T) {
	usecase := &stubUsecase{}
	h := NewWebhookHandler(usecase, defaultProviders())

	req := httptest.NewRequest(http.MethodPost, "/inbound/", bytes.NewBufferString(`{"tenant_id":"`+tenantA+`"}`))
	req.Header.Set("Content-Type", "application/json")
	if rec := serve(h, req); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
	if len(usecase.received) != 0 {
		t.Error("a post to the bare path was processed")
	}
}

// --- payload formats ---

// The multipart branch called r.ParseMultipartForm after the body had already
// been read, so it always failed, silently, and an empty message was stored
// and answered with 202.
func TestAMultipartPostIsParsed(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, value := range map[string]string{
		"from":    "Sender <sender@example.com>",
		"to":      "box@example.org, other@example.org",
		"subject": "Hello",
		"text":    "plain body",
		"html":    "<p>html body</p>",
		"headers": "Message-ID: <abc@example.com>\nX-Spam-Score: 0.1\n",
	} {
		mw.WriteField(name, value)
	}
	mw.Close()
	body := buf.Bytes()

	usecase := &stubUsecase{}
	h := NewWebhookHandler(usecase, defaultProviders())
	req := httptest.NewRequest(http.MethodPost, inboundPath(tenantA, providerA), bytes.NewReader(body))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(SignatureHeader, sign(secretA, body))

	if rec := serve(h, req); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(usecase.received) != 1 {
		t.Fatalf("processed %d messages, want 1", len(usecase.received))
	}
	got := usecase.received[0]
	if got.From != "Sender <sender@example.com>" || got.Subject != "Hello" ||
		got.BodyText != "plain body" || got.BodyHtml != "<p>html body</p>" {
		t.Errorf("parsed %+v", got)
	}
	if len(got.To) != 2 || got.To[0] != "box@example.org" || got.To[1] != "other@example.org" {
		t.Errorf("to = %q", got.To)
	}
	if got.Headers["Message-Id"] != "<abc@example.com>" {
		t.Errorf("headers = %v", got.Headers)
	}
}

func TestAPostThatYieldsNoMessageIsRefused(t *testing.T) {
	cases := []struct{ name, contentType, body string }{
		{name: "empty JSON object", contentType: "application/json", body: `{}`},
		{name: "malformed JSON", contentType: "application/json", body: `{"from":`},
		{name: "multipart with no boundary", contentType: "multipart/form-data", body: "--x\r\n"},
		{name: "multipart that does not parse", contentType: "multipart/form-data; boundary=x", body: "not multipart"},
		{name: "no content type", contentType: "", body: `{"from":"a@b.c"}`},
		{name: "unsupported content type", contentType: "text/plain", body: "from: a@b.c"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usecase := &stubUsecase{}
			h := NewWebhookHandler(usecase, defaultProviders())

			req := httptest.NewRequest(http.MethodPost, inboundPath(tenantA, providerA), bytes.NewBufferString(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			req.Header.Set(SignatureHeader, sign(secretA, []byte(tc.body)))
			rec := serve(h, req)

			if rec.Code < 400 || rec.Code >= 500 {
				t.Errorf("status %d, want a 4xx", rec.Code)
			}
			if len(usecase.received) != 0 {
				t.Error("a post with no message in it was processed")
			}
		})
	}
}

// --- failure reporting ---

// It used to return the usecase's error verbatim, and that usecase writes to
// the inbound store, the outbox and any webhook subscriptions, so the text
// could carry filesystem paths, the database user and host, or an internal
// endpoint's address.
//
// Third instance of this shape, after /admin/backup and /readyz.
func TestAFailedInboundDoesNotDescribeWhyToTheCaller(t *testing.T) {
	internal := errors.New(
		"pebble: /var/lib/panmail/inbound.db: write failed; " +
			"outbox: failed to connect to `user=panmail database=panmail`: db.internal:5432")

	h := NewWebhookHandler(&stubUsecase{err: internal}, defaultProviders())
	rec := signedJSON(t, h, `{"from":"a@b.c","to":["d@e.f"],"subject":"s"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 so the provider retries", rec.Code)
	}

	body := strings.ToLower(rec.Body.String())
	for _, leaked := range []string{
		"/var/lib/panmail", // where things are on disk
		"pebble",           // what the storage engine is
		"user=panmail",     // the database user
		"db.internal",      // the internal hostname
		"5432",             // and its port
	} {
		if strings.Contains(body, strings.ToLower(leaked)) {
			t.Errorf("the response published %q to the caller: %s", leaked, rec.Body.String())
		}
	}
}

// The status code still has to be right, because it is what the provider uses
// to decide whether to redeliver. Saying less must not mean saying nothing.
func TestAFailedInboundStillAsksForARetry(t *testing.T) {
	h := NewWebhookHandler(&stubUsecase{err: errors.New("boom")}, defaultProviders())
	rec := signedJSON(t, h, `{"from":"a@b.c","to":["d@e.f"]}`)

	if rec.Code < 500 {
		t.Errorf("status %d: a provider reads anything below 500 as accepted and drops the message", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) == "" {
		t.Error("an empty body gives an operator reading a provider's delivery log nothing at all")
	}
}

func TestAnAcceptedInboundSucceeds(t *testing.T) {
	h := NewWebhookHandler(&stubUsecase{}, defaultProviders())
	rec := signedJSON(t, h, `{"from":"a@b.c","to":["d@e.f"],"subject":"hello"}`)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Errorf("status %d for a message that was processed", rec.Code)
	}
}

func TestOnlyPostIsAccepted(t *testing.T) {
	h := NewWebhookHandler(&stubUsecase{}, defaultProviders())
	rec := serve(h, httptest.NewRequest(http.MethodGet, inboundPath(tenantA, providerA), nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET returned %d, want 405", rec.Code)
	}
}
