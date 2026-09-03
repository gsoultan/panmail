package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// fakeScreener answers with a fixed decision and records what it was asked to
// store, which is enough to pin every branch of the hook.
type fakeScreener struct {
	decision  emailfilter.Decision
	screenErr error
	calls     int
	recorded  []emailfilter.FilteredMessage
	seen      emailfilter.Message
}

func (f *fakeScreener) Screen(_ context.Context, _ string, _ emailfilter.Direction, m emailfilter.Message) (emailfilter.Decision, error) {
	f.calls++
	f.seen = m
	return f.decision, f.screenErr
}

func (f *fakeScreener) Record(_ context.Context, record *emailfilter.FilteredMessage) error {
	f.recorded = append(f.recorded, *record)
	return nil
}

func decisionOf(action emailfilter.Action, name string) emailfilter.Decision {
	return emailfilter.Decision{
		Action: action,
		Rule:   &emailfilter.Rule{ID: "rule-1", Name: name, Action: action},
	}
}

type filterHarness struct {
	usecase  SendEmailUsecase
	outbox   *mockOutboxRepo
	screener *fakeScreener
	worker   *countingWorker
}

type countingWorker struct{ triggers int }

func (w *countingWorker) Start(context.Context)                    {}
func (w *countingWorker) Trigger()                                 { w.triggers++ }
func (w *countingWorker) SetRetention(time.Duration)               {}
func (w *countingWorker) SetRetryPatternSource(RetryPatternSource) {}

func newFilterHarness(t *testing.T, screener *fakeScreener) *filterHarness {
	t.Helper()

	outbox := &mockOutboxRepo{}
	worker := &countingWorker{}

	// A typed nil pointer in an interface is not a nil interface, so assigning
	// (*fakeScreener)(nil) straight into the field would make the hook believe
	// it had a screener and call through a nil pointer. The "no screener" case
	// has to be a genuinely nil interface.
	var configured emailfilter.Screener
	if screener != nil {
		configured = screener
	}
	u := NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo: &mockProviderRepo{provider: &providerEntities.EmailProvider{
			ID: testProviderID, TenantID: testTenantID, Name: "test", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		}},
		TemplateRepo:    &mockTemplateRepo{},
		SuppressionRepo: &mockSuppressionRepo{},
		OutboxRepo:      outbox,
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: &mockSender{}},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
		Screener:        configured,
	})
	if registrar, ok := u.(interface{ RegisterQueueWorker(QueueWorker) }); ok {
		registrar.RegisterQueueWorker(worker)
	}
	return &filterHarness{usecase: u, outbox: outbox, screener: screener, worker: worker}
}

func filterRequest() *panmailv1.SendEmailRequest {
	return &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "user@example.com",
		To:         []string{"to@example.com"},
		Subject:    "Hello",
		BodyText:   "World",
	}
}

// Filtering that is not configured must send mail exactly as it did before.
func TestSendWithoutAScreenerIsUnchanged(t *testing.T) {
	h := newFilterHarness(t, nil)

	if _, err := h.usecase.SendEmail(context.Background(), testTenantID, filterRequest()); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	if len(h.outbox.emails) != 1 || h.outbox.emails[0].Status != entities.OutboxStatusPending {
		t.Fatalf("outbox = %+v, want one PENDING row", h.outbox.emails)
	}
}

func TestAHeldMessageWaitsInTheOutboxAndDoesNotWakeTheWorker(t *testing.T) {
	screener := &fakeScreener{decision: decisionOf(emailfilter.ActionHold, "big attachments")}
	h := newFilterHarness(t, screener)

	res, err := h.usecase.SendEmail(context.Background(), testTenantID, filterRequest())
	if err != nil {
		t.Fatalf("SendEmail: %v", err)
	}

	// The payload is durable, and invisible to the worker because the claim
	// query only takes PENDING, DEFERRED and expired SENDING rows.
	if len(h.outbox.emails) != 1 {
		t.Fatalf("outbox rows = %d, want the payload kept", len(h.outbox.emails))
	}
	if got := h.outbox.emails[0].Status; got != entities.OutboxStatusHeld {
		t.Errorf("outbox status = %q, want HELD", got)
	}
	// Waking the worker for a message it must not touch is wasted work at
	// best, and an invitation to a claim-query bug at worst.
	if h.worker.triggers != 0 {
		t.Errorf("worker triggered %d times for a held message, want 0", h.worker.triggers)
	}

	if len(screener.recorded) != 1 {
		t.Fatalf("recorded %d decisions, want 1", len(screener.recorded))
	}
	record := screener.recorded[0]
	if record.Status != emailfilter.StatusPending {
		t.Errorf("status = %q, want PENDING review", record.Status)
	}
	if record.PayloadRef != res.MessageId {
		t.Errorf("payload ref = %q, want the outbox id %q", record.PayloadRef, res.MessageId)
	}
	if record.RuleName != "big attachments" {
		t.Errorf("rule name = %q, want it copied onto the record", record.RuleName)
	}
}

// A rejection nobody is told about is a message that silently vanished.
func TestARejectedMessageFailsTheCallAndIsNeverQueued(t *testing.T) {
	screener := &fakeScreener{decision: decisionOf(emailfilter.ActionReject, "executable attachment")}
	h := newFilterHarness(t, screener)

	_, err := h.usecase.SendEmail(context.Background(), testTenantID, filterRequest())
	if err == nil {
		t.Fatal("SendEmail accepted a message a rule rejected")
	}
	if !strings.Contains(err.Error(), "executable attachment") {
		t.Errorf("error = %v, want it to name the rule", err)
	}
	if len(h.outbox.emails) != 0 {
		t.Errorf("outbox rows = %d, want none for a rejected message", len(h.outbox.emails))
	}
	if len(screener.recorded) != 1 {
		t.Errorf("recorded %d decisions, want the rejection logged", len(screener.recorded))
	}
}

func TestATaggedMessageIsDeliveredAndRecorded(t *testing.T) {
	screener := &fakeScreener{decision: decisionOf(emailfilter.ActionTag, "watch list")}
	h := newFilterHarness(t, screener)

	if _, err := h.usecase.SendEmail(context.Background(), testTenantID, filterRequest()); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	if len(h.outbox.emails) != 1 || h.outbox.emails[0].Status != entities.OutboxStatusPending {
		t.Errorf("a tagged message did not queue normally: %+v", h.outbox.emails)
	}
	if h.worker.triggers != 1 {
		t.Errorf("worker triggered %d times, want 1", h.worker.triggers)
	}
	if len(screener.recorded) != 1 {
		t.Errorf("recorded %d decisions, want the tag logged", len(screener.recorded))
	}
}

// A gateway that sends everything whenever its database is briefly unreachable
// is a filter that fails open at the one moment it was supposed to work.
func TestAScreeningFailureRefusesTheSend(t *testing.T) {
	screener := &fakeScreener{screenErr: errors.New("database unreachable")}
	h := newFilterHarness(t, screener)

	if _, err := h.usecase.SendEmail(context.Background(), testTenantID, filterRequest()); err == nil {
		t.Fatal("a screening failure let the message through")
	}
	if len(h.outbox.emails) != 0 {
		t.Errorf("outbox rows = %d, want none when screening failed", len(h.outbox.emails))
	}
}

// The trap this whole placement exists to avoid. The outbox worker calls
// SendEmail a second time to deliver; screening on that pass would hold a
// released message again the instant it was let go, and nothing would leave.
func TestTheOutboxWorkerPassIsNotScreened(t *testing.T) {
	screener := &fakeScreener{decision: decisionOf(emailfilter.ActionHold, "would re-hold")}
	h := newFilterHarness(t, screener)

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	ctx = context.WithValue(ctx, MessageIDKey, "already-queued")
	_, _ = h.usecase.SendEmail(ctx, testTenantID, filterRequest())

	if screener.calls != 0 {
		t.Errorf("the worker pass was screened %d times, want 0 — a released message would be held again", screener.calls)
	}
}

// A rule about the subject or the body has to see what the recipient will see,
// so screening happens after the template is rendered.
func TestScreeningSeesTheRenderedMessage(t *testing.T) {
	screener := &fakeScreener{}
	h := newFilterHarness(t, screener)

	req := filterRequest()
	req.Subject = "Invoice 42"
	req.BodyText = "Please pay"
	req.Attachments = []*panmailv1.Attachment{{Filename: "invoice.pdf", Content: []byte("%PDF")}}

	if _, err := h.usecase.SendEmail(context.Background(), testTenantID, req); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}

	if screener.seen.Subject != "Invoice 42" {
		t.Errorf("subject = %q, want the rendered subject", screener.seen.Subject)
	}
	if len(screener.seen.Attachments) != 1 || screener.seen.Attachments[0].Filename != "invoice.pdf" {
		t.Errorf("attachments = %+v, want them projected for the rules", screener.seen.Attachments)
	}
	if screener.seen.ProviderID != testProviderID {
		t.Errorf("provider = %q, want it available to outbound rules", screener.seen.ProviderID)
	}
}

func (f *fakeScreener) SetRetention(time.Duration) {}
