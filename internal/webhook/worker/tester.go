package worker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
)

// TestHeader marks a notification sent from the "Send test" action, so a
// receiver can tell it apart from a real one without parsing the body.
const TestHeader = "X-Panmail-Test"

// Tester posts one notification to a subscription right away and reports how
// the endpoint answered.
//
// It goes through the same client the delivery worker uses -- the same
// redirect rule, the same egress restriction, the same signature -- so a test
// that passes means a real delivery would be received the same way. Nothing is
// queued: a test is for seeing the answer while setting an endpoint up, and
// retrying it behind the user's back would only confuse that.
type Tester struct {
	client *http.Client
}

// TestResult is what the endpoint did with a test notification. Err is set
// when no answer came back at all; StatusCode is zero then.
type TestResult struct {
	StatusCode int
	Body       string
	Err        error
	Duration   time.Duration
}

// NewTester builds a Tester with the delivery worker's client settings.
func NewTester() *Tester {
	return &Tester{client: newDeliveryClient(defaultDeliveryTimeout, false)}
}

// RestrictEgress refuses test sends to private and reserved addresses, as
// DurableWorker.RestrictEgress does for real ones.
func (t *Tester) RestrictEgress() {
	t.client = newDeliveryClient(t.client.Timeout, true)
}

// Send posts body to the subscription's URL, signed with its secret.
func (t *Tester) Send(ctx context.Context, sub *entities.Webhook, event string, body []byte) TestResult {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(body))
	if err != nil {
		return TestResult{Err: err, Duration: time.Since(start)}
	}
	applySignature(req, sub.Secret, event, uuid.New().String(), body, time.Now())
	req.Header.Set(TestHeader, "true")

	resp, err := t.client.Do(req)
	if err != nil {
		return TestResult{Err: err, Duration: time.Since(start)}
	}
	defer resp.Body.Close()
	// Bounded for the same reason as a real delivery: the body is only kept
	// to show why an endpoint refused.
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	return TestResult{
		StatusCode: resp.StatusCode,
		Body:       string(answer),
		Duration:   time.Since(start),
	}
}
