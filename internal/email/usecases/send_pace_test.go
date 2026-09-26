package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	evententities "github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/ratelimit"
	"github.com/gsoultan/panmail/pkg/tracking"
)

func pacedProvider(t *testing.T, perMinute, burst int32) *providerEntities.EmailProvider {
	t.Helper()
	p := smtpProvider(t, "smtp.example.net", nil)
	p.SendRatePerMinute = perMinute
	p.SendBurst = burst
	return p
}

type paceHarness struct {
	u       SendEmailUsecase
	sender  *mockSender
	events  *mockEventUsecase
	limiter *ratelimit.Limiter
}

func newPaceHarness(provider *providerEntities.EmailProvider, tenant ratelimit.Limit) *paceHarness {
	h := &paceHarness{sender: &mockSender{}, events: &mockEventUsecase{}, limiter: ratelimit.New()}
	h.u = NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: provider},
		TemplateRepo:    &mockTemplateRepo{},
		SuppressionRepo: &mockSuppressionRepo{},
		OutboxRepo:      &mockOutboxRepo{},
		EventUsecase:    h.events,
		ProviderFactory: &mockFactory{sender: h.sender},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
		Limiter:         h.limiter,
		SendLimits:      &stubLimits{limit: tenant},
	})
	return h
}

func paceRequest(to ...string) *panmailv1.SendEmailRequest {
	return &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "noreply@example.com",
		To:         to,
		Subject:    "x",
		BodyHtml:   "<html><body>x</body></html>",
	}
}

// deliver runs the worker's half of SendEmail for one message id.
func (h *paceHarness) deliver(messageID string, req *panmailv1.SendEmailRequest) error {
	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	ctx = context.WithValue(ctx, MessageIDKey, messageID)
	_, err := h.u.SendEmail(ctx, testTenantID, req)
	return err
}

func TestDeliveryWithinTheCeilingIsNotPaced(t *testing.T) {
	h := newPaceHarness(pacedProvider(t, 60, 5), ratelimit.Limit{})

	if err := h.deliver("msg-1", paceRequest("a@example.com")); err != nil {
		t.Fatalf("delivery within the burst was refused: %v", err)
	}
	if len(h.sender.sentEmails) != 1 {
		t.Fatalf("sent %d, want 1", len(h.sender.sentEmails))
	}
}

// The case the ceiling is for: a backlog draining after an outage used to
// leave at the worker's full concurrency, straight through it.
func TestALongWaitIsRescheduledRatherThanSlept(t *testing.T) {
	// Six a minute: a token every ten seconds, well past maxPaceWait.
	h := newPaceHarness(pacedProvider(t, 6, 1), ratelimit.Limit{})

	if err := h.deliver("msg-1", paceRequest("a@example.com")); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	start := time.Now()
	err := h.deliver("msg-2", paceRequest("b@example.com"))
	elapsed := time.Since(start)

	var paced *DeliveryPacedError
	if !errors.As(err, &paced) {
		t.Fatalf("error = %v, want DeliveryPacedError", err)
	}
	if paced.RetryAfter < 9*time.Second || paced.RetryAfter > 11*time.Second {
		t.Errorf("retry after = %v, want about the ten seconds a token takes", paced.RetryAfter)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v: a wait this long must be rescheduled, not slept through", elapsed)
	}
	if len(h.sender.sentEmails) != 1 {
		t.Errorf("sent %d, want only the first", len(h.sender.sentEmails))
	}
}

// A short wait is cheaper to sleep through than to reschedule.
func TestAShortWaitIsSleptThrough(t *testing.T) {
	// Six hundred a minute: a token every 100ms.
	h := newPaceHarness(pacedProvider(t, 600, 1), ratelimit.Limit{})

	if err := h.deliver("msg-1", paceRequest("a@example.com")); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	start := time.Now()
	if err := h.deliver("msg-2", paceRequest("b@example.com")); err != nil {
		t.Fatalf("a short wait was refused rather than slept: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("took %v: the second delivery did not wait its turn", elapsed)
	}
	if len(h.sender.sentEmails) != 2 {
		t.Errorf("sent %d, want 2", len(h.sender.sentEmails))
	}
}

// The regression guard. SendEmail runs once to admit and once to deliver, and
// charging one bucket on both sides once halved every configured rate. With a
// burst of one, a single message admitted and then delivered must go out at
// once: if the two shared a bucket, the delivery would find it already spent.
func TestAdmissionAndDeliveryDoNotShareABucket(t *testing.T) {
	provider := pacedProvider(t, 6, 1)
	h := newPaceHarness(provider, ratelimit.Limit{PerMinute: 6, Burst: 1})
	req := paceRequest("a@example.com")

	if _, err := h.u.SendEmail(context.Background(), testTenantID, req); err != nil {
		t.Fatalf("admission: %v", err)
	}
	if err := h.deliver("msg-1", req); err != nil {
		t.Fatalf("delivery was charged against the admission bucket: %v", err)
	}
	if len(h.sender.sentEmails) != 1 {
		t.Fatalf("sent %d, want 1", len(h.sender.sentEmails))
	}
}

// A retry of a partly delivered message is charged for what is left, not for
// the whole list again.
func TestARetryIsNotChargedForRecipientsAlreadyDelivered(t *testing.T) {
	provider := pacedProvider(t, 6, 3)
	h := newPaceHarness(provider, ratelimit.Limit{})

	// Two of three already went out on an earlier attempt.
	for _, r := range []string{"a@example.com", "b@example.com"} {
		h.events.events = append(h.events.events, &evententities.EmailEvent{
			TenantID: testTenantID, MessageID: "msg-1", Recipient: r,
			Type: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
		})
	}
	// Leave one token. Charging all three recipients would put the bucket
	// two in debt, twenty seconds at this rate, and pace the retry.
	h.limiter.ReserveAll([]ratelimit.Request{{
		Key:   egressBucketKey(testTenantID, provider.ID),
		Limit: providerSendLimit(provider),
	}}, 2)

	if err := h.deliver("msg-1", paceRequest("a@example.com", "b@example.com", "c@example.com")); err != nil {
		t.Fatalf("the retry was charged for recipients already delivered: %v", err)
	}
	if len(h.sender.sentEmails) != 1 {
		t.Errorf("sent %d, want only the one still pending", len(h.sender.sentEmails))
	}
}

// The tenant ceiling was bypassed by the drain too, not only the provider's.
func TestDeliveryIsPacedByTheTenantCeilingToo(t *testing.T) {
	h := newPaceHarness(pacedProvider(t, 0, 0), ratelimit.Limit{PerMinute: 6, Burst: 1})

	if err := h.deliver("msg-1", paceRequest("a@example.com")); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	var paced *DeliveryPacedError
	if err := h.deliver("msg-2", paceRequest("b@example.com")); !errors.As(err, &paced) {
		t.Fatalf("error = %v, want the tenant ceiling to pace the drain", err)
	}
}

// Zero is unlimited and zero is the default: nothing already running is paced.
func TestNoCeilingMeansNoPacing(t *testing.T) {
	h := newPaceHarness(pacedProvider(t, 0, 0), ratelimit.Limit{})

	for i := range 20 {
		if err := h.deliver("msg", paceRequest("a@example.com")); err != nil {
			t.Fatalf("delivery %d paced with no ceiling set: %v", i+1, err)
		}
	}
	if h.limiter.Tracked() != 0 {
		t.Errorf("tracked %d buckets with no ceiling set, want 0", h.limiter.Tracked())
	}
}

// A delivery whose context ends while it waits gives the slot back and is
// rescheduled, rather than failing or holding a turn it will not use.
func TestAWaitCutShortGivesTheSlotBack(t *testing.T) {
	provider := pacedProvider(t, 60, 1) // a token a second
	h := newPaceHarness(provider, ratelimit.Limit{})

	if err := h.deliver("msg-1", paceRequest("a@example.com")); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx = context.WithValue(ctx, SkipOutboxKey, true)
	ctx = context.WithValue(ctx, MessageIDKey, "msg-2")
	_, err := h.u.SendEmail(ctx, testTenantID, paceRequest("b@example.com"))

	var paced *DeliveryPacedError
	if !errors.As(err, &paced) {
		t.Fatalf("error = %v, want DeliveryPacedError when the wait is cut short", err)
	}

	// The slot went back: the next reservation queues behind msg-1 only.
	d := h.limiter.ReserveAll([]ratelimit.Request{{
		Key:   egressBucketKey(testTenantID, provider.ID),
		Limit: providerSendLimit(provider),
	}}, 1).Delay()
	if d > time.Second {
		t.Errorf("delay = %v: the abandoned slot was not given back", d)
	}
}
