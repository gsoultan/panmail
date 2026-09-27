package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/ratelimit"
	suppressionentities "github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// selectiveSuppressions suppresses exactly the keys it holds, and can be told to
// fail, which the all-or-nothing mock elsewhere cannot express.
type selectiveSuppressions struct {
	mockSuppressionRepo
	suppressed map[string]string
	err        error
}

func (s *selectiveSuppressions) GetByEmails(_ context.Context, _ string, emails []string) (map[string]*suppressionentities.Suppression, error) {
	if s.err != nil {
		return nil, s.err
	}
	found := map[string]*suppressionentities.Suppression{}
	for _, e := range emails {
		if reason, ok := s.suppressed[e]; ok {
			found[e] = &suppressionentities.Suppression{Email: e, Reason: reason}
		}
	}
	return found, nil
}

type deliveryHarness struct {
	u       SendEmailUsecase
	sender  *mockSender
	events  *mockEventUsecase
	sup     *selectiveSuppressions
	limiter *ratelimit.Limiter
}

func newDeliveryHarness(t *testing.T) *deliveryHarness {
	h := &deliveryHarness{
		sender:  &mockSender{},
		events:  &mockEventUsecase{},
		sup:     &selectiveSuppressions{suppressed: map[string]string{}},
		limiter: ratelimit.New(),
	}
	p := smtpProvider(t, "smtp.example.net", nil)
	p.SendRatePerMinute, p.SendBurst = 60, 60
	h.u = NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: p},
		TemplateRepo:    &mockTemplateRepo{},
		SuppressionRepo: h.sup,
		OutboxRepo:      &mockOutboxRepo{},
		EventUsecase:    h.events,
		ProviderFactory: &mockFactory{sender: h.sender},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
		Limiter:         h.limiter,
		SendLimits:      &stubLimits{},
	})
	return h
}

func deliveryRequest(to ...string) *panmailv1.SendEmailRequest {
	return &panmailv1.SendEmailRequest{
		ProviderId: testProviderID, From: "noreply@example.com",
		To: to, Subject: "Your receipt", BodyHtml: "<html><body>x</body></html>",
	}
}

func (h *deliveryHarness) deliver(req *panmailv1.SendEmailRequest) error {
	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	ctx = context.WithValue(ctx, MessageIDKey, "msg-1")
	_, err := h.u.SendEmail(ctx, testTenantID, req)
	return err
}

// sentTo is who actually received a copy: the SMTP envelope, not the To header.
// Each copy carries the message's full original addressing in its headers, so
// a recipient dropped at delivery still appears in the others' To line; what
// matters is that no copy is enveloped to them.
func (h *deliveryHarness) sentTo() []string {
	var out []string
	for _, e := range h.sender.sentEmails {
		out = append(out, e.Envelope...)
	}
	return out
}

func (h *deliveryHarness) droppedFor(recipient string) bool {
	for _, e := range h.events.events {
		if e.Recipient == recipient && e.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DROPPED {
			return true
		}
	}
	return false
}

// The case that shipped: a customer who unsubscribes while their message sits
// in the outbox still received it.
func TestARecipientSuppressedAfterAdmissionIsNotSent(t *testing.T) {
	h := newDeliveryHarness(t)
	req := deliveryRequest("customer@example.com")

	if _, err := h.u.SendEmail(context.Background(), testTenantID, req); err != nil {
		t.Fatalf("admission: %v", err)
	}
	h.sup.suppressed["customer@example.com"] = "unsubscribed"

	if err := h.deliver(req); err != nil {
		t.Fatalf("delivery: %v -- a dropped recipient is not a failure", err)
	}
	if got := h.sentTo(); len(got) != 0 {
		t.Fatalf("sent to %v after they were suppressed", got)
	}
	if !h.droppedFor("customer@example.com") {
		t.Error("no DROPPED event, so the tenant's webhook would never hear why")
	}
}

// Admission refuses the whole message; delivery cannot, because the message
// was accepted long ago and the other recipients still want it.
func TestOnlyTheSuppressedRecipientIsDropped(t *testing.T) {
	h := newDeliveryHarness(t)
	h.sup.suppressed["gone@example.com"] = "hard bounce"

	if err := h.deliver(deliveryRequest("a@example.com", "gone@example.com", "b@example.com")); err != nil {
		t.Fatalf("delivery: %v", err)
	}

	got := strings.Join(h.sentTo(), ",")
	if strings.Contains(got, "gone@example.com") {
		t.Errorf("sent to the suppressed recipient: %s", got)
	}
	if !strings.Contains(got, "a@example.com") || !strings.Contains(got, "b@example.com") {
		t.Errorf("sent to %s, want the two recipients who were not suppressed", got)
	}
	if !h.droppedFor("gone@example.com") {
		t.Error("the suppressed recipient has no DROPPED event")
	}
}

// Delivery's recipient list is only lowercased, so it still carries a display
// name; a suppression is stored under the bare address. Looking up the list
// as-is would never match, and the suppression would be silently ignored.
func TestADisplayNameRecipientIsMatchedToItsSuppression(t *testing.T) {
	h := newDeliveryHarness(t)
	h.sup.suppressed["bob@example.com"] = "unsubscribed"

	if err := h.deliver(deliveryRequest("Bob <Bob@Example.com>")); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if got := h.sentTo(); len(got) != 0 {
		t.Fatalf("sent to %v: a display-name recipient slipped past its suppression", got)
	}
}

// Every recipient suppressed is a message with nothing left to do, not a failure
// to retry.
func TestAMessageWhoseRecipientsAreAllSuppressedCompletes(t *testing.T) {
	h := newDeliveryHarness(t)
	h.sup.suppressed["a@example.com"] = "unsubscribed"
	h.sup.suppressed["b@example.com"] = "complaint"

	if err := h.deliver(deliveryRequest("a@example.com", "b@example.com")); err != nil {
		t.Fatalf("delivery: %v, want the message to complete", err)
	}
	if got := h.sentTo(); len(got) != 0 {
		t.Fatalf("sent to %v", got)
	}
}

// Sending because the list could not be read would mail exactly the person who
// asked to be left alone. The lookup failing is returned, so the worker retries.
func TestAFailedSuppressionLookupSendsNothing(t *testing.T) {
	h := newDeliveryHarness(t)
	h.sup.err = errors.New("database unreachable")

	if err := h.deliver(deliveryRequest("a@example.com")); err == nil {
		t.Fatal("delivery went ahead without being able to read the suppression list")
	}
	if got := h.sentTo(); len(got) != 0 {
		t.Fatalf("sent to %v with the suppression list unreadable", got)
	}
}

// A recipient who will not be sent to must not spend the pace.
func TestADroppedRecipientDoesNotSpendThePace(t *testing.T) {
	h := newDeliveryHarness(t)
	for _, r := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		h.sup.suppressed[r] = "unsubscribed"
	}

	if err := h.deliver(deliveryRequest("a@example.com", "b@example.com", "c@example.com")); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if h.limiter.Tracked() != 0 {
		t.Errorf("tracked %d buckets: suppressed recipients were charged", h.limiter.Tracked())
	}
}

// A recipient already delivered on an earlier attempt is finished. Suppressing
// them afterwards must not produce a DROPPED event that contradicts it.
func TestAnAlreadyDeliveredRecipientIsNotRechecked(t *testing.T) {
	h := newDeliveryHarness(t)
	_ = h.u.RecordEvent(context.Background(), testTenantID, testProviderID, "msg-1",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, "done@example.com", "", "", nil)
	h.sup.suppressed["done@example.com"] = "unsubscribed"

	if err := h.deliver(deliveryRequest("done@example.com")); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if h.droppedFor("done@example.com") {
		t.Error("a recipient already delivered was recorded as dropped")
	}
}
