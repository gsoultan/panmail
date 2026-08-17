package panmail

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
)

// apiKeyHeader is what the gateway reads a key from. It is deliberately not
// Authorization: that header carries a session token, and a key sent as a
// bearer token is rejected as a malformed session rather than as a bad key.
const apiKeyHeader = "X-API-Key"

// Client sends mail through a panmail gateway.
//
// Safe for concurrent use, and worth keeping for the life of the process: it
// holds an HTTP client whose connection pool is the reason a second send is
// faster than the first.
type Client struct {
	emails panmailv1connect.EmailServiceClient
	opts   options
}

// New builds a client for the gateway at baseURL, authenticating with apiKey.
//
// baseURL is the gateway's origin — "https://mail.example.com" — not a path to
// a procedure.
func New(baseURL, apiKey string, opts ...Option) (*Client, error) {
	if err := validateBaseURL(baseURL); err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, errors.New("panmail: an api key is required")
	}

	resolved := newOptions(opts)
	clientOptions := append(
		[]connect.ClientOption{connect.WithInterceptors(apiKeyInterceptor(apiKey))},
		resolved.connectOptions...,
	)

	return &Client{
		emails: panmailv1connect.NewEmailServiceClient(
			resolved.httpClient,
			strings.TrimRight(baseURL, "/"),
			clientOptions...,
		),
		opts: resolved,
	}, nil
}

// Send queues a message and returns once the gateway has it on disk.
//
// A nil error means the gateway accepted responsibility for delivering the
// message, not that it has been delivered — that is reported afterwards
// through delivery events and webhooks, keyed by Result.MessageID.
//
// An error is either a refusal the caller can act on — see RateLimitedError,
// BacklogFullError and AuthError — or the transport error underneath. A send
// that fails with an unknown outcome is not retried; see the package
// documentation for why.
func (c *Client) Send(ctx context.Context, msg Message) (Result, error) {
	req, err := msg.request()
	if err != nil {
		return Result{}, err
	}

	for attempt := 0; ; attempt++ {
		res, err := c.emails.SendEmail(ctx, connect.NewRequest(req))
		if err == nil {
			return Result{MessageID: res.Msg.MessageId, Status: res.Msg.Status}, nil
		}

		classified := classify(err)
		wait, ok := c.waitBefore(classified, attempt)
		if !ok {
			return Result{}, classified
		}
		if err := sleep(ctx, wait); err != nil {
			// The caller's deadline outranks the gateway's suggestion.
			return Result{}, errors.Join(classified, err)
		}
	}
}

// waitBefore reports how long to hold before repeating a send, and whether to
// repeat it at all.
//
// Only a rate refusal qualifies, and only when the gateway named a delay. Any
// other failure either means the message was accepted, or means nobody can
// tell — and repeating those is how one send becomes two deliveries.
func (c *Client) waitBefore(err error, attempt int) (time.Duration, bool) {
	if attempt >= c.opts.rateLimitRetries {
		return 0, false
	}

	var limited *RateLimitedError
	if !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		return 0, false
	}
	return limited.RetryAfter, true
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func apiKeyInterceptor(apiKey string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set(apiKeyHeader, apiKey)
			return next(ctx, req)
		}
	}
}

func validateBaseURL(baseURL string) error {
	if baseURL == "" {
		return errors.New("panmail: a base url is required")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("panmail: base url is not a url: %w", err)
	}
	// A missing scheme is the usual mistake — "mail.example.com" parses
	// happily as a relative path, and the failure it causes surfaces much
	// later as an unreadable transport error.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("panmail: base url needs an http or https scheme, got %q", baseURL)
	}
	if parsed.Host == "" {
		return fmt.Errorf("panmail: base url has no host: %q", baseURL)
	}
	return nil
}
