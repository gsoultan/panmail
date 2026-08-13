package worker

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// What a receiver is telling you decides whether to try again. A 5xx means the
// endpoint is broken now and will probably recover; a 4xx means it looked at
// the request and rejected it, and retrying an unchanged request against an
// unchanged opinion only keeps hitting an endpoint that has said no.

func TestServerErrorsAreRetried(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		if !retryable(status) {
			t.Errorf("%d should be retried; the endpoint is broken, not the request", status)
		}
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 410, 422} {
		if retryable(status) {
			t.Errorf("%d should not be retried; the receiver has already rejected it", status)
		}
	}
}

// The two codes that mean "not now" rather than "not ever". Treating a request
// to slow down as permanent failure is the opposite of what was asked.
func TestTimeoutAndTooManyRequestsAreRetried(t *testing.T) {
	if !retryable(http.StatusRequestTimeout) {
		t.Error("408 is a timeout, which is exactly what retrying is for")
	}
	if !retryable(http.StatusTooManyRequests) {
		t.Error("429 asks to slow down, not to stop")
	}
}

func TestTheRetryScheduleIsFrontLoadedThenGivesUp(t *testing.T) {
	first, ok := nextAttempt(1)
	if !ok || first > time.Minute {
		t.Errorf("first retry after %v; most failures are a restart and clear within a minute", first)
	}

	last, ok := nextAttempt(len(retrySchedule))
	if !ok || last < time.Hour {
		t.Errorf("last retry after %v; an endpoint down for an afternoon should still be caught", last)
	}

	// Bounded, or a permanently dead endpoint is retried forever.
	if _, ok := nextAttempt(len(retrySchedule) + 1); ok {
		t.Error("the schedule never gives up")
	}
	if _, ok := nextAttempt(0); ok {
		t.Error("attempt 0 should not produce a delay")
	}
}

func TestTheScheduleOnlyGrows(t *testing.T) {
	var previous time.Duration
	for i := 1; i <= len(retrySchedule); i++ {
		d, _ := nextAttempt(i)
		if d <= previous {
			t.Errorf("attempt %d waits %v, not longer than the %v before it", i, d, previous)
		}
		previous = d
	}
}

// --- signing ---

func TestASignatureIsStableForTheSameInput(t *testing.T) {
	a := sign("secret", 1000, []byte(`{"event":"x"}`))
	b := sign("secret", 1000, []byte(`{"event":"x"}`))
	if a != b {
		t.Error("the same notification signed twice produced different signatures")
	}
	if a[:7] != "sha256=" {
		t.Errorf("signature %q is not prefixed with its scheme", a)
	}
}

func TestADifferentBodyChangesTheSignature(t *testing.T) {
	if sign("secret", 1000, []byte(`{"a":1}`)) == sign("secret", 1000, []byte(`{"a":2}`)) {
		t.Error("the body is not covered by the signature")
	}
}

func TestADifferentSecretChangesTheSignature(t *testing.T) {
	body := []byte(`{"a":1}`)
	if sign("one", 1000, body) == sign("two", 1000, body) {
		t.Error("the secret does not affect the signature, so anyone could forge one")
	}
}

// Signing only the body means a notification captured once can be replayed
// forever with a signature that still verifies.
func TestTheTimestampIsCoveredSoACaptureCannotBeReplayed(t *testing.T) {
	body := []byte(`{"a":1}`)
	if sign("secret", 1000, body) == sign("secret", 2000, body) {
		t.Error("the timestamp is not signed, so a captured notification replays forever")
	}
}

func TestTheRequestCarriesWhatAReceiverNeedsToVerify(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/hook", nil)
	body := []byte(`{"event":"mail.bounced"}`)
	now := time.Unix(1_700_000_000, 0)

	applySignature(req, "secret", "mail.bounced", "delivery-1", body, now)

	if got := req.Header.Get(SignatureHeader); got != sign("secret", now.Unix(), body) {
		t.Errorf("signature header = %q, does not match the body it accompanies", got)
	}
	if got := req.Header.Get(TimestampHeader); got != strconv.FormatInt(now.Unix(), 10) {
		t.Errorf("timestamp header = %q; without it the receiver cannot check the signature", got)
	}
	if req.Header.Get(EventHeader) != "mail.bounced" {
		t.Error("the event header lets a receiver route without parsing the body")
	}
	// Stable across retries, which is what lets a receiver make its own
	// handling idempotent.
	if req.Header.Get(DeliveryHeader) != "delivery-1" {
		t.Error("the delivery id is missing, so a receiver cannot deduplicate retries")
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Error("content type is not set")
	}
}

// Subscriptions that predate signing have no secret. Refusing to deliver to
// them would turn a security improvement into an outage for every existing
// integration.
func TestASubscriptionWithNoSecretIsStillDelivered(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/hook", nil)
	applySignature(req, "", "mail.sent", "delivery-1", []byte(`{}`), time.Now())

	if req.Header.Get(SignatureHeader) != "" {
		t.Error("an unsigned delivery should carry no signature header rather than an empty one")
	}
	if req.Header.Get(EventHeader) != "mail.sent" {
		t.Error("the other headers should still be set")
	}
}
