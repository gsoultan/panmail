package oauth2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/gsmail"
)

// capture records what the token endpoint was actually asked for, which is the
// half of the exchange the tests need to assert on.
type capture struct {
	Form        url.Values
	ContentType string
}

func serve(t *testing.T, status int, body string, cap *capture) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if cap != nil {
			cap.Form = r.Form
			cap.ContentType = r.Header.Get("Content-Type")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func baseConfig(endpoint string) Config {
	return Config{
		TokenEndpoint: endpoint,
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		RefreshToken:  "refresh-token",
	}
}

func TestASuccessfulRefreshReturnsTheTokenAndItsExpiry(t *testing.T) {
	var cap capture
	srv := serve(t, http.StatusOK, `{"access_token":"ya29.token","expires_in":3600}`, &cap)

	before := time.Now()
	token, expiry, err := RefreshFunc(baseConfig(srv.URL))(context.Background())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if token != "ya29.token" {
		t.Errorf("token = %q", token)
	}

	// The expiry must be in the future and roughly match expires_in, or the
	// caching layer renews at the wrong time.
	if d := expiry.Sub(before); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("expiry is %v from now, expected about an hour", d)
	}

	if got := cap.Form.Get("grant_type"); got != "refresh_token" {
		t.Errorf("grant_type = %q", got)
	}
	if got := cap.Form.Get("refresh_token"); got != "refresh-token" {
		t.Errorf("refresh_token = %q", got)
	}
	if !strings.HasPrefix(cap.ContentType, "application/x-www-form-urlencoded") {
		t.Errorf("content type = %q", cap.ContentType)
	}
}

// A provider that omits expires_in must not produce a zero expiry, which the
// caching layer would read as already expired and refresh on every single send.
func TestAMissingExpiryFallsBackToAShortLifetime(t *testing.T) {
	srv := serve(t, http.StatusOK, `{"access_token":"tok"}`, nil)

	_, expiry, err := RefreshFunc(baseConfig(srv.URL))(context.Background())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !expiry.After(time.Now().Add(time.Minute)) {
		t.Errorf("expiry %v is too soon; every send would trigger a refresh", expiry)
	}
}

// A revoked or expired refresh token is permanent. Retrying re-sends the same
// rejected credential every time the outbox wakes, so it must not be retryable.
func TestARejectedGrantIsPermanent(t *testing.T) {
	srv := serve(t, http.StatusBadRequest,
		`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`, nil)

	_, _, err := RefreshFunc(baseConfig(srv.URL))(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if gsmail.IsRetryable(err) {
		t.Error("a rejected grant must not be retried")
	}
	// The message has to tell the operator what to actually do.
	if !strings.Contains(err.Error(), "reauthorise") {
		t.Errorf("the error should say the provider needs reauthorising, got: %v", err)
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("the provider's description should be surfaced, got: %v", err)
	}
}

// A server-side fault is worth retrying: the credential is fine and the outbox
// will come back to it.
func TestAServerFaultIsRetryable(t *testing.T) {
	srv := serve(t, http.StatusServiceUnavailable, `{"error":"temporarily_unavailable"}`, nil)

	_, _, err := RefreshFunc(baseConfig(srv.URL))(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !gsmail.IsRetryable(err) {
		t.Error("a 5xx from the identity provider should be retried")
	}
}

// 200 with no token is a broken provider, not a token worth caching. Returning
// an empty string would make every send authenticate with nothing.
func TestAnEmptyTokenIsRefused(t *testing.T) {
	srv := serve(t, http.StatusOK, `{"expires_in":3600}`, nil)

	_, _, err := RefreshFunc(baseConfig(srv.URL))(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if gsmail.IsRetryable(err) {
		t.Error("a 200 with no access token will not fix itself")
	}
}

// Configuration errors must fail immediately rather than being retried against
// an endpoint that cannot possibly succeed.
func TestIncompleteConfigurationIsRefusedWithoutARequest(t *testing.T) {
	cases := map[string]Config{
		"no endpoint":      {ClientID: "c", RefreshToken: "r"},
		"no client id":     {TokenEndpoint: "https://x.test", RefreshToken: "r"},
		"no refresh token": {TokenEndpoint: "https://x.test", ClientID: "c"},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			// No server: reaching the network at all would be the failure.
			_, _, err := RefreshFunc(cfg)(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			if gsmail.IsRetryable(err) {
				t.Error("a configuration error must not be retried")
			}
			if !strings.Contains(err.Error(), "incomplete") {
				t.Errorf("the error should name what is missing, got: %v", err)
			}
		})
	}
}

// A public client has no secret, and sending an empty one makes some providers
// reject the request.
func TestAnEmptyClientSecretIsOmittedEntirely(t *testing.T) {
	var cap capture
	srv := serve(t, http.StatusOK, `{"access_token":"t","expires_in":60}`, &cap)

	cfg := baseConfig(srv.URL)
	cfg.ClientSecret = ""
	if _, _, err := RefreshFunc(cfg)(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, present := cap.Form["client_secret"]; present {
		t.Error("an empty client_secret was sent")
	}
}

func TestScopeIsSentOnlyWhenSet(t *testing.T) {
	var cap capture
	srv := serve(t, http.StatusOK, `{"access_token":"t","expires_in":60}`, &cap)

	cfg := baseConfig(srv.URL)
	cfg.Scope = "https://mail.google.com/"
	if _, _, err := RefreshFunc(cfg)(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := cap.Form.Get("scope"); got != "https://mail.google.com/" {
		t.Errorf("scope = %q", got)
	}
}

// A hung identity provider must not block a send forever.
func TestARequestHonoursItsContext(t *testing.T) {
	// The handler blocks until the test releases it. Blocking on the request
	// context instead deadlocks: httptest.Server.Close waits for outstanding
	// handlers, and the server-side context is not necessarily cancelled just
	// because the client gave up. Cleanups run last-registered-first, so the
	// release below runs before Close.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := RefreshFunc(baseConfig(srv.URL))(ctx)
	if err == nil {
		t.Fatal("expected the request to be abandoned")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected a context error, got: %v", err)
	}
}
