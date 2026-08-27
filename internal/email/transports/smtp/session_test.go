package smtp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	gosmtpclient "github.com/emersion/go-smtp"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/email/usecases"
)

const (
	testTenant   = "tenant-1"
	testKeyID    = "key-1"
	testAPIKey   = "pm_test_key"
	testProvider = "3f1c2b7a-0000-4000-8000-000000000001"
)

// stubVerifier resolves one known key.
type stubVerifier struct {
	scopes []entities.Scope
	err    error
}

func (s *stubVerifier) VerifyApiKey(_ context.Context, key string) (*entities.ApiKey, error) {
	if s.err != nil {
		return nil, s.err
	}
	if key != testAPIKey {
		return nil, errors.New("api key not found")
	}
	scopes := s.scopes
	if scopes == nil {
		scopes = []entities.Scope{entities.ScopeEmailSend}
	}
	return &entities.ApiKey{
		ID: testKeyID, TenantID: testTenant, Scopes: scopes, IsEnabled: true,
	}, nil
}

// stubSender records what the transport handed the pipeline.
type stubSender struct {
	mu       sync.Mutex
	tenantID string
	requests []*panmailv1.SendEmailRequest
	err      error
}

func (s *stubSender) SendEmail(
	_ context.Context, tenantID string, req *panmailv1.SendEmailRequest,
) (*panmailv1.SendEmailResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenantID = tenantID
	if s.err != nil {
		return nil, s.err
	}
	s.requests = append(s.requests, req)
	return &panmailv1.SendEmailResponse{MessageId: "msg-1"}, nil
}

func (s *stubSender) last() *panmailv1.SendEmailRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return nil
	}
	return s.requests[len(s.requests)-1]
}

func (s *stubSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// startTestServer runs a listener on loopback and returns its address.
//
// AllowInsecureAuth is set because the test speaks plaintext to a socket that
// never leaves the machine. It is the setting a deployment has to justify.
func startTestServer(t *testing.T, verifier APIKeyVerifier, sender Sender) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv, err := NewServer(Config{
		Addr:              listener.Addr().String(),
		Domain:            "test",
		AllowInsecureAuth: true,
		MaxRecipients:     5,
	}, verifier, sender, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(listener)
	}()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
	})

	return listener.Addr().String()
}

// submit runs one full SMTP conversation and returns the server's verdict.
func submit(t *testing.T, addr, username, password, from string, to []string, body string) error {
	t.Helper()

	client, err := gosmtpclient.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Auth(sasl.NewPlainClient("", username, password)); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := client.Mail(from, nil); err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt, nil); err != nil {
			return fmt.Errorf("rcpt: %w", err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := io.WriteString(writer, strings.ReplaceAll(body, "\n", "\r\n")); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}
	return nil
}

func TestSubmissionReachesTheSendPipeline(t *testing.T) {
	sender := &stubSender{}
	addr := startTestServer(t, &stubVerifier{}, sender)

	body := `From: app@example.com
To: user@example.net
Subject: Hello

hi there
`
	if err := submit(t, addr, testProvider, testAPIKey, "app@example.com",
		[]string{"user@example.net"}, body); err != nil {
		t.Fatalf("submit: %v", err)
	}

	req := sender.last()
	if req == nil {
		t.Fatal("nothing reached the pipeline")
	}
	if sender.tenantID != testTenant {
		t.Errorf("tenant = %q, want %q", sender.tenantID, testTenant)
	}
	if req.ProviderId != testProvider {
		t.Errorf("ProviderId = %q, want %q", req.ProviderId, testProvider)
	}
	if req.From != "app@example.com" {
		t.Errorf("From = %q", req.From)
	}
	if req.Subject != "Hello" {
		t.Errorf("Subject = %q", req.Subject)
	}
}

// The provider header must win, so one authenticated connection can send
// through more than one provider.
func TestProviderHeaderOverridesTheAuthUsername(t *testing.T) {
	const other = "3f1c2b7a-0000-4000-8000-000000000002"

	sender := &stubSender{}
	addr := startTestServer(t, &stubVerifier{}, sender)

	body := `From: app@example.com
To: user@example.net
Subject: Routed
X-Panmail-Provider-Id: ` + other + `

hi
`
	if err := submit(t, addr, testProvider, testAPIKey, "app@example.com",
		[]string{"user@example.net"}, body); err != nil {
		t.Fatalf("submit: %v", err)
	}

	if got := sender.last().ProviderId; got != other {
		t.Errorf("ProviderId = %q, want %q", got, other)
	}
}

// The envelope holds every recipient; the headers hold only the visible ones.
// The difference is the Bcc, and it must not leak into To or Cc.
func TestEnvelopeOnlyRecipientsBecomeBcc(t *testing.T) {
	sender := &stubSender{}
	addr := startTestServer(t, &stubVerifier{}, sender)

	body := `From: app@example.com
To: visible@example.net
Cc: copied@example.net
Subject: Blind

hi
`
	envelope := []string{"visible@example.net", "copied@example.net", "blind@example.net"}
	if err := submit(t, addr, testProvider, testAPIKey, "app@example.com", envelope, body); err != nil {
		t.Fatalf("submit: %v", err)
	}

	req := sender.last()
	if !equalStrings(req.To, []string{"visible@example.net"}) {
		t.Errorf("To = %v, want [visible@example.net]", req.To)
	}
	if !equalStrings(req.Cc, []string{"copied@example.net"}) {
		t.Errorf("Cc = %v, want [copied@example.net]", req.Cc)
	}
	if !equalStrings(req.Bcc, []string{"blind@example.net"}) {
		t.Errorf("Bcc = %v, want [blind@example.net]", req.Bcc)
	}
}

func TestSubmissionIsRefusedWithoutValidCredentials(t *testing.T) {
	testCases := []struct {
		name     string
		username string
		password string
		scopes   []entities.Scope
	}{
		{
			name:     "unknown key",
			username: testProvider,
			password: "not-the-key",
		},
		{
			name:     "key without the send scope",
			username: testProvider,
			password: testAPIKey,
			scopes:   []entities.Scope{entities.ScopeEventsRead},
		},
		{
			name:     "provider id that is not a uuid",
			username: "not-a-uuid",
			password: testAPIKey,
		},
		{
			name:     "nil uuid provider id",
			username: "00000000-0000-0000-0000-000000000000",
			password: testAPIKey,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sender := &stubSender{}
			addr := startTestServer(t, &stubVerifier{scopes: tc.scopes}, sender)

			body := "From: app@example.com\nTo: user@example.net\nSubject: x\n\nhi\n"
			err := submit(t, addr, tc.username, tc.password, "app@example.com",
				[]string{"user@example.net"}, body)
			if err == nil {
				t.Fatal("submit succeeded, want a refusal")
			}
			if sender.count() != 0 {
				t.Errorf("%d messages reached the pipeline, want 0", sender.count())
			}
		})
	}
}

// A message with no provider anywhere must be refused permanently: retrying it
// unchanged can never succeed.
func TestSubmissionWithoutAProviderIsRefusedPermanently(t *testing.T) {
	sender := &stubSender{}
	addr := startTestServer(t, &stubVerifier{}, sender)

	body := "From: app@example.com\nTo: user@example.net\nSubject: x\n\nhi\n"
	err := submit(t, addr, "", testAPIKey, "app@example.com", []string{"user@example.net"}, body)
	if err == nil {
		t.Fatal("submit succeeded, want a refusal")
	}
	assertReplyCode(t, err, 501)
}

// Capacity refusals must be temporary, or a client discards mail the gateway
// merely asked it to slow down about.
func TestCapacityRefusalsAreTemporary(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "over the send rate",
			err:      &usecases.RateLimitedError{TenantID: testTenant, RetryAfter: 3 * time.Second},
			wantCode: codeRateLimited,
		},
		{
			name:     "backlog full",
			err:      &usecases.BacklogFullError{TenantID: testTenant, Pending: 500, Ceiling: 100},
			wantCode: codeBacklogFull,
		},
		{
			name:     "unrecognised failure",
			err:      errors.New("database is unreachable"),
			wantCode: codeTemporaryFailure,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sender := &stubSender{err: tc.err}
			addr := startTestServer(t, &stubVerifier{}, sender)

			body := "From: app@example.com\nTo: user@example.net\nSubject: x\n\nhi\n"
			err := submit(t, addr, testProvider, testAPIKey, "app@example.com",
				[]string{"user@example.net"}, body)
			if err == nil {
				t.Fatal("submit succeeded, want a refusal")
			}
			assertReplyCode(t, err, tc.wantCode)
		})
	}
}

func TestTooManyRecipientsIsRefused(t *testing.T) {
	sender := &stubSender{}
	addr := startTestServer(t, &stubVerifier{}, sender)

	recipients := []string{
		"a@example.net", "b@example.net", "c@example.net",
		"d@example.net", "e@example.net", "f@example.net",
	}
	body := "From: app@example.com\nTo: a@example.net\nSubject: x\n\nhi\n"
	err := submit(t, addr, testProvider, testAPIKey, "app@example.com", recipients, body)
	if err == nil {
		t.Fatal("submit succeeded, want a refusal")
	}
	if sender.count() != 0 {
		t.Errorf("%d messages reached the pipeline, want 0", sender.count())
	}
}

// A config that would put API keys on the wire in the clear must fail loudly
// at startup rather than quietly at runtime.
func TestNewServerRefusesAuthInTheClearByDefault(t *testing.T) {
	_, err := NewServer(Config{Addr: "127.0.0.1:0"}, &stubVerifier{}, &stubSender{}, nil)
	if !errors.Is(err, ErrInsecureAuthWithoutTLS) {
		t.Fatalf("error = %v, want ErrInsecureAuthWithoutTLS", err)
	}
}

func assertReplyCode(t *testing.T, err error, want int) {
	t.Helper()

	var smtpErr *gosmtpclient.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("error %v is not an SMTPError", err)
	}
	if smtpErr.Code != want {
		t.Errorf("reply code = %d, want %d (message %q)", smtpErr.Code, want, smtpErr.Message)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
