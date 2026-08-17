package panmail

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"connectrpc.com/connect"
)

// RateLimitedError reports that the tenant is sending faster than its
// configured rate allows. The message was not accepted, so sending it again
// after RetryAfter is safe.
type RateLimitedError struct {
	RetryAfter time.Duration
	err        error
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("panmail: send rate exceeded, retry after %s: %v", e.RetryAfter, e.err)
}

func (e *RateLimitedError) Unwrap() error { return e.err }

// BacklogFullError reports that the tenant already has more queued than its
// own send rate can drain in the near future. The message was not accepted.
//
// Unlike a rate refusal this carries no delay, because there is none to give:
// a queue clearing is not something a caller can schedule against. Retrying on
// a timer will not help, and retrying immediately makes the wait longer for
// everything already queued. Slow down, or stop.
type BacklogFullError struct {
	err error
}

func (e *BacklogFullError) Error() string {
	return fmt.Sprintf("panmail: the tenant's queue is too deep to accept more: %v", e.err)
}

func (e *BacklogFullError) Unwrap() error { return e.err }

// AuthError reports that the API key was missing, rejected, or lacks the
// email:send scope.
type AuthError struct {
	err error
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("panmail: the api key was not accepted: %v", e.err)
}

func (e *AuthError) Unwrap() error { return e.err }

// classify turns a transport error into something a caller can act on.
//
// The gateway answers both of its capacity refusals with ResourceExhausted,
// deliberately: they are the same answer to the client — you are asking for
// more than you may have. What separates them is Retry-After, which the rate
// limiter sets and the backlog check does not, because only one of the two has
// a delay worth quoting. That is the discrimination here, and it is why
// removing Retry-After from the rate refusal would silently reclassify every
// rate limit as a full queue.
func classify(err error) error {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return err
	}

	switch connectErr.Code() {
	case connect.CodeResourceExhausted:
		if retryAfter, ok := retryAfter(connectErr); ok {
			return &RateLimitedError{RetryAfter: retryAfter, err: err}
		}
		return &BacklogFullError{err: err}
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return &AuthError{err: err}
	default:
		return err
	}
}

func retryAfter(connectErr *connect.Error) (time.Duration, bool) {
	value := connectErr.Meta().Get("Retry-After")
	if value == "" {
		return 0, false
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 {
		// A header this client cannot read is not a reason to call the refusal
		// something else: it is still a rate limit, just one with no usable
		// delay attached.
		return 0, true
	}
	return time.Duration(seconds) * time.Second, true
}
