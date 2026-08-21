package panmail

import (
	"net/http"
	"time"

	"connectrpc.com/connect"
)

// DefaultTimeout bounds a single send. Generous, because the gateway writes
// the message to its outbox before answering and that is a disk write on a
// possibly busy database — but finite, because a send that hangs holds
// whatever request is waiting on it.
const DefaultTimeout = 30 * time.Second

// An Option configures the client.
type Option func(*options)

type options struct {
	httpClient       *http.Client
	timeout          time.Duration
	rateLimitRetries int
	connectOptions   []connect.ClientOption
}

// WithHTTPClient supplies the HTTP client to send with — for a custom
// transport, a proxy, or a connection pool shared with the rest of an
// application. Its own Timeout is left alone; WithTimeout is ignored when this
// is set, since a caller who brings a client has already made that decision.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) { o.httpClient = client }
}

// WithTimeout bounds each send. Ignored when WithHTTPClient is used.
//
// This is a ceiling, not a schedule: a context deadline shorter than it still
// wins.
func WithTimeout(timeout time.Duration) Option {
	return func(o *options) { o.timeout = timeout }
}

// WithRateLimitRetries waits out up to n rate-limit refusals, sleeping for the
// delay the gateway asks for each time.
//
// Off by default, and only ever applied to a refusal. A refusal is the one
// failure where the client knows the message was not accepted, so repeating it
// cannot deliver anything twice — but it does mean Send blocks for as long as
// the gateway asks, which inside a request handler is a decision the caller
// should make rather than inherit. A queue of your own is usually the better
// answer.
func WithRateLimitRetries(n int) Option {
	return func(o *options) { o.rateLimitRetries = n }
}

// WithConnectOptions passes options through to the underlying ConnectRPC
// client — compression, the gRPC protocol instead of Connect, interceptors of
// your own.
func WithConnectOptions(opts ...connect.ClientOption) Option {
	return func(o *options) { o.connectOptions = append(o.connectOptions, opts...) }
}

func newOptions(opts []Option) options {
	resolved := options{timeout: DefaultTimeout}
	for _, opt := range opts {
		opt(&resolved)
	}
	if resolved.httpClient == nil {
		resolved.httpClient = &http.Client{Timeout: resolved.timeout}
	}
	if resolved.rateLimitRetries < 0 {
		resolved.rateLimitRetries = 0
	}
	return resolved
}
