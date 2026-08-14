package http

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type stubUsecase struct{ err error }

func (s stubUsecase) Process(context.Context, *panmailv1.InboundEmail) error { return s.err }
func (s stubUsecase) List(context.Context, string, int, string) ([]*panmailv1.InboundEmail, string, error) {
	return nil, "", nil
}
func (s stubUsecase) Get(context.Context, string, string) (*panmailv1.InboundEmail, error) {
	return nil, nil
}

func post(t *testing.T, h *WebhookHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/inbound/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// This endpoint is public and unauthenticated — it has to be, because the
// provider posting to it cannot hold a credential. It used to return the
// usecase's error verbatim, and that usecase writes to the inbound store, the
// outbox and any webhook subscriptions, so the text could carry filesystem
// paths, the database user and host, or an internal endpoint's address.
//
// Third instance of this shape, after /admin/backup and /readyz.
func TestAFailedInboundDoesNotDescribeWhyToTheCaller(t *testing.T) {
	internal := errors.New(
		"pebble: /var/lib/panmail/inbound.db: write failed; " +
			"outbox: failed to connect to `user=panmail database=panmail`: db.internal:5432")

	h := NewWebhookHandler(stubUsecase{err: internal})
	rec := post(t, h, `{"tenant_id":"t1","from":"a@b.c","to":["d@e.f"],"subject":"s"}`)

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
			t.Errorf("the response published %q to an anonymous caller: %s", leaked, rec.Body.String())
		}
	}
}

// The status code still has to be right, because it is what the provider uses
// to decide whether to redeliver. Saying less must not mean saying nothing.
func TestAFailedInboundStillAsksForARetry(t *testing.T) {
	h := NewWebhookHandler(stubUsecase{err: errors.New("boom")})
	rec := post(t, h, `{"tenant_id":"t1","from":"a@b.c","to":["d@e.f"]}`)

	if rec.Code < 500 {
		t.Errorf("status %d: a provider reads anything below 500 as accepted and drops the message", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) == "" {
		t.Error("an empty body gives an operator reading a provider's delivery log nothing at all")
	}
}

func TestAnAcceptedInboundSucceeds(t *testing.T) {
	h := NewWebhookHandler(stubUsecase{})
	rec := post(t, h, `{"tenant_id":"t1","from":"a@b.c","to":["d@e.f"],"subject":"hello"}`)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Errorf("status %d for a message that was processed", rec.Code)
	}
}

func TestOnlyPostIsAccepted(t *testing.T) {
	h := NewWebhookHandler(stubUsecase{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/inbound/", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET returned %d, want 405", rec.Code)
	}
}
