package panmail

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
)

// stubService is the gateway's side of the wire: a real Connect handler, so
// the tests exercise headers, encoding and error metadata rather than a mock
// of the client's own idea of them.
type stubService struct {
	panmailv1connect.UnimplementedEmailServiceHandler

	calls    atomic.Int64
	apiKeys  chan string
	requests chan *panmailv1.SendEmailRequest

	// respond returns the error to answer with, or nil to accept.
	respond func(attempt int64) error
}

func (s *stubService) SendEmail(
	_ context.Context,
	req *connect.Request[panmailv1.SendEmailRequest],
) (*connect.Response[panmailv1.SendEmailResponse], error) {
	attempt := s.calls.Add(1)

	select {
	case s.apiKeys <- req.Header().Get(apiKeyHeader):
	default:
	}
	select {
	case s.requests <- req.Msg:
	default:
	}

	if s.respond != nil {
		if err := s.respond(attempt); err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&panmailv1.SendEmailResponse{
		MessageId: "msg-1",
		Status:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING,
	}), nil
}

// gateway starts a stub gateway and returns it with a client pointed at it.
func gateway(t *testing.T, respond func(attempt int64) error, opts ...Option) (*stubService, *Client) {
	t.Helper()

	service := &stubService{
		respond:  respond,
		apiKeys:  make(chan string, 8),
		requests: make(chan *panmailv1.SendEmailRequest, 8),
	}

	mux := http.NewServeMux()
	mux.Handle(panmailv1connect.NewEmailServiceHandler(service))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := New(server.URL, "key-123", opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service, client
}

func validMessage() Message {
	return Message{
		ProviderID: "0f8b8f4e-0000-4000-8000-000000000000",
		From:       "noreply@example.com",
		To:         []string{"someone@example.org"},
		Subject:    "Your receipt",
		HTML:       "<p>Thanks.</p>",
	}
}

func TestSend(t *testing.T) {
	service, client := gateway(t, nil)

	result, err := client.Send(t.Context(), validMessage())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.MessageID != "msg-1" {
		t.Errorf("message id = %q; want msg-1", result.MessageID)
	}
	if result.Status != StatusPending {
		t.Errorf("status = %v; want pending", result.Status)
	}

	// The key goes in X-API-Key, not Authorization: sent as a bearer token it
	// is rejected as a malformed session rather than as a bad key.
	if got := <-service.apiKeys; got != "key-123" {
		t.Errorf("api key header = %q; want key-123", got)
	}
}

func TestSendCarriesTheWholeMessage(t *testing.T) {
	service, client := gateway(t, nil)

	msg := validMessage()
	msg.Cc = []string{"cc@example.org"}
	msg.Bcc = []string{"bcc@example.org"}
	msg.Text = "Thanks."
	msg.TemplateData = map[string]any{"order": "A-1", "total": 12.5}
	msg.Attachments = []Attachment{{Filename: "receipt.pdf", Content: []byte("%PDF")}}

	if _, err := client.Send(t.Context(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := <-service.requests
	if got.From != msg.From || len(got.To) != 1 || len(got.Cc) != 1 || len(got.Bcc) != 1 {
		t.Errorf("addresses did not survive the wire: %+v", got)
	}
	if got.BodyHtml != msg.HTML || got.BodyText != msg.Text {
		t.Errorf("bodies did not survive the wire: html=%q text=%q", got.BodyHtml, got.BodyText)
	}
	if got.TemplateData.GetFields()["order"].GetStringValue() != "A-1" {
		t.Errorf("template data = %v; want the order field", got.TemplateData)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %d; want 1", len(got.Attachments))
	}
	// Guessed from the extension, because an attachment sent as
	// application/octet-stream is one every mail client offers to download
	// rather than show.
	if ct := got.Attachments[0].ContentType; ct != "application/pdf" {
		t.Errorf("content type = %q; want application/pdf", ct)
	}
}

func TestSendRejectsBadMessagesWithoutCallingTheGateway(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Message)
	}{
		{"no provider", func(m *Message) { m.ProviderID = "" }},
		{"no from", func(m *Message) { m.From = "" }},
		{"no recipients", func(m *Message) { m.To = nil }},
		{"nothing to say", func(m *Message) { m.HTML, m.Text, m.TemplateID = "", "", "" }},
		{"attachment with no name", func(m *Message) {
			m.Attachments = []Attachment{{Content: []byte("x")}}
		}},
		{"template data that is not json", func(m *Message) {
			m.TemplateData = map[string]any{"when": time.Now()}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, client := gateway(t, nil)

			msg := validMessage()
			tc.mutate(&msg)

			if _, err := client.Send(t.Context(), msg); err == nil {
				t.Fatal("accepted a message the gateway would refuse")
			}
			// Caught here rather than after a round trip, so the error names
			// the field instead of quoting the gateway.
			if service.calls.Load() != 0 {
				t.Errorf("called the gateway %d times; want 0", service.calls.Load())
			}
		})
	}
}

func rateLimited(seconds int) error {
	err := connect.NewError(connect.CodeResourceExhausted, errors.New("send rate exceeded"))
	err.Meta().Set("Retry-After", strconv.Itoa(seconds))
	return err
}

func backlogFull() error {
	return connect.NewError(connect.CodeResourceExhausted, errors.New("queue is too deep"))
}

func TestSendClassifiesRefusals(t *testing.T) {
	tests := []struct {
		name   string
		from   error
		assert func(*testing.T, error)
	}{
		{
			name: "rate limited",
			from: rateLimited(3),
			assert: func(t *testing.T, err error) {
				var limited *RateLimitedError
				if !errors.As(err, &limited) {
					t.Fatalf("error = %v; want a RateLimitedError", err)
				}
				if limited.RetryAfter != 3*time.Second {
					t.Errorf("retry after = %v; want 3s", limited.RetryAfter)
				}
			},
		},
		{
			// Same code, no Retry-After. The gateway sends no delay for a full
			// queue on purpose, because a queue clearing is not something a
			// caller can schedule against — so the absence is the signal.
			name: "backlog full",
			from: backlogFull(),
			assert: func(t *testing.T, err error) {
				var full *BacklogFullError
				if !errors.As(err, &full) {
					t.Fatalf("error = %v; want a BacklogFullError", err)
				}
				var limited *RateLimitedError
				if errors.As(err, &limited) {
					t.Error("a full queue was reported as a rate limit")
				}
			},
		},
		{
			name: "bad key",
			from: connect.NewError(connect.CodeUnauthenticated, errors.New("invalid api key")),
			assert: func(t *testing.T, err error) {
				var authErr *AuthError
				if !errors.As(err, &authErr) {
					t.Fatalf("error = %v; want an AuthError", err)
				}
			},
		},
		{
			name: "missing the send scope",
			from: connect.NewError(connect.CodePermissionDenied, errors.New("api key is missing the \"email:send\" scope")),
			assert: func(t *testing.T, err error) {
				var authErr *AuthError
				if !errors.As(err, &authErr) {
					t.Fatalf("error = %v; want an AuthError", err)
				}
			},
		},
		{
			// Anything else is handed back as it came: inventing a category
			// for an unfamiliar failure tells the caller something nobody
			// checked.
			name: "anything else",
			from: connect.NewError(connect.CodeInternal, errors.New("provider not found")),
			assert: func(t *testing.T, err error) {
				for _, unwanted := range []any{
					new(*RateLimitedError), new(*BacklogFullError), new(*AuthError),
				} {
					if errors.As(err, unwanted) {
						t.Errorf("an internal error was classified as %T", unwanted)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, client := gateway(t, func(int64) error { return tc.from })

			_, err := client.Send(t.Context(), validMessage())
			if err == nil {
				t.Fatal("Send succeeded despite the gateway refusing")
			}
			tc.assert(t, err)
		})
	}
}

func TestSendDoesNotRetryByDefault(t *testing.T) {
	// The whole point: a send whose outcome is unknown is not repeated, and
	// even a refusal is not repeated unless the caller asked for it.
	service, client := gateway(t, func(int64) error { return rateLimited(1) })

	if _, err := client.Send(t.Context(), validMessage()); err == nil {
		t.Fatal("Send succeeded despite the gateway refusing")
	}
	if got := service.calls.Load(); got != 1 {
		t.Errorf("gateway called %d times; want 1", got)
	}
}

func TestSendWaitsOutARateLimitWhenAsked(t *testing.T) {
	service, client := gateway(t,
		func(attempt int64) error {
			if attempt == 1 {
				return rateLimited(1)
			}
			return nil
		},
		WithRateLimitRetries(2),
	)

	start := time.Now()
	if _, err := client.Send(t.Context(), validMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := service.calls.Load(); got != 2 {
		t.Errorf("gateway called %d times; want 2", got)
	}
	// Waited the delay the gateway asked for rather than retrying straight
	// away, which is what makes an overload worse.
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("retried after %v; want at least the 1s the gateway asked for", elapsed)
	}
}

func TestSendGivesUpAfterTheConfiguredRetries(t *testing.T) {
	service, client := gateway(t,
		func(int64) error { return rateLimited(1) },
		WithRateLimitRetries(1),
	)

	if _, err := client.Send(t.Context(), validMessage()); err == nil {
		t.Fatal("Send succeeded despite every attempt being refused")
	}
	if got := service.calls.Load(); got != 2 {
		t.Errorf("gateway called %d times; want the original plus one retry", got)
	}
}

func TestSendDoesNotRetryAFullQueue(t *testing.T) {
	// Retrying on a timer cannot help, and retrying at once makes the wait
	// longer for everything already queued — so the retry budget does not
	// apply to this refusal even though it shares a code with the other one.
	service, client := gateway(t,
		func(int64) error { return backlogFull() },
		WithRateLimitRetries(3),
	)

	if _, err := client.Send(t.Context(), validMessage()); err == nil {
		t.Fatal("Send succeeded despite the queue being full")
	}
	if got := service.calls.Load(); got != 1 {
		t.Errorf("gateway called %d times; want 1", got)
	}
}

func TestSendStopsWaitingWhenTheCallerGivesUp(t *testing.T) {
	// A gateway asking for longer than the caller has must not turn Send into
	// a call that outlives its own deadline.
	_, client := gateway(t,
		func(int64) error { return rateLimited(math.MaxInt32) },
		WithRateLimitRetries(1),
	)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.Send(ctx, validMessage())
	if err == nil {
		t.Fatal("Send succeeded despite the context expiring")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v; want it to report the expired deadline", err)
	}
	// The refusal is kept alongside it: "deadline exceeded" alone would hide
	// that the gateway had already said why.
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Errorf("error = %v; want the rate limit to survive alongside the deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %v; want the caller's deadline to win", elapsed)
	}
}

func TestNewValidatesItsInputs(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		apiKey  string
	}{
		{name: "no base url", baseURL: "", apiKey: "key"},
		{name: "no api key", baseURL: "https://mail.example.com", apiKey: ""},
		// The usual mistake. It parses happily as a relative path and fails
		// much later as an unreadable transport error.
		{name: "no scheme", baseURL: "mail.example.com", apiKey: "key"},
		{name: "wrong scheme", baseURL: "ftp://mail.example.com", apiKey: "key"},
		{name: "no host", baseURL: "https://", apiKey: "key"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.baseURL, tc.apiKey); err == nil {
				t.Error("accepted a configuration that cannot work")
			}
		})
	}
}

func TestNewAcceptsABaseURLWithATrailingSlash(t *testing.T) {
	service := &stubService{apiKeys: make(chan string, 1), requests: make(chan *panmailv1.SendEmailRequest, 1)}
	mux := http.NewServeMux()
	mux.Handle(panmailv1connect.NewEmailServiceHandler(service))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := New(server.URL+"/", "key-123")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A doubled slash in the procedure path is a 404 from most proxies, which
	// reads as "the gateway does not implement this method".
	if _, err := client.Send(t.Context(), validMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
}
