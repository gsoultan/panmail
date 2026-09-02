package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
)

type stubRepo struct {
	written  []*entities.InboundEmail
	writeErr error
	found    *entities.InboundEmail
}

func (s *stubRepo) Write(_ context.Context, e *entities.InboundEmail) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.written = append(s.written, e)
	return nil
}
func (s *stubRepo) List(context.Context, string, int, string) ([]*entities.InboundEmail, string, error) {
	return nil, "", nil
}
func (s *stubRepo) GetByID(_ context.Context, _, _ string) (*entities.InboundEmail, error) {
	if s.found == nil {
		// De-duplication treats a lookup failure as not-seen, which is what
		// every test that is not about release wants.
		return nil, errors.New("not found")
	}
	return s.found, nil
}
func (s *stubRepo) Count(context.Context, string, time.Time, time.Time) (int64, error) {
	return 0, nil
}
func (s *stubRepo) TruncateBefore(context.Context, time.Time) (int64, error) { return 0, nil }
func (s *stubRepo) Close() error                                             { return nil }

type stubTrigger struct{ enqueued int }

func (s *stubTrigger) Enqueue(string, panmailv1.WebhookTriggerEvent, any) { s.enqueued++ }

type stubScreener struct {
	decision  emailfilter.Decision
	screenErr error
	calls     int
	recorded  []emailfilter.FilteredMessage
	seen      emailfilter.Message
}

func (s *stubScreener) Screen(_ context.Context, _ string, _ emailfilter.Direction, m emailfilter.Message) (emailfilter.Decision, error) {
	s.calls++
	s.seen = m
	return s.decision, s.screenErr
}
func (s *stubScreener) Record(_ context.Context, r *emailfilter.FilteredMessage) error {
	s.recorded = append(s.recorded, *r)
	return nil
}

func inboundOf() *panmailv1.InboundEmail {
	return &panmailv1.InboundEmail{
		Id: "msg-1", TenantId: "tenant-1",
		From: "sender@outside.example", To: []string{"support@example.com"},
		Subject: "Invoice", BodyText: "please see attached",
		Headers: map[string]string{"X-Spam-Score": "9.1"},
	}
}

func harness(t *testing.T, screener *stubScreener) (InboundUsecase, *stubRepo, *stubTrigger) {
	t.Helper()
	repo := &stubRepo{}
	trigger := &stubTrigger{}
	u := NewInboundUsecase(repo, nil, trigger)
	if screener != nil {
		u = u.(interface {
			WithScreener(emailfilter.Screener) InboundUsecase
		}).WithScreener(screener)
	}
	return u, repo, trigger
}

func decisionOf(action emailfilter.Action, name string) emailfilter.Decision {
	return emailfilter.Decision{Action: action, Rule: &emailfilter.Rule{ID: "r1", Name: name, Action: action}}
}

// A held message is still stored. The mail has arrived; losing it would be
// worse than showing it to a reviewer. What the hold suppresses is the webhook.
func TestAHeldInboundMessageIsStoredButNotAnnounced(t *testing.T) {
	screener := &stubScreener{decision: decisionOf(emailfilter.ActionHold, "external invoice")}
	u, repo, trigger := harness(t, screener)

	if err := u.Process(context.Background(), inboundOf()); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(repo.written) != 1 {
		t.Errorf("stored %d messages, want the mail kept", len(repo.written))
	}
	if trigger.enqueued != 0 {
		t.Errorf("webhook fired %d times for a held message, want 0", trigger.enqueued)
	}
	if len(screener.recorded) != 1 || screener.recorded[0].RuleName != "external invoice" {
		t.Errorf("recorded = %+v, want the hold logged with its rule", screener.recorded)
	}
}

// No error on a rejection: the message was already accepted over SMTP, and an
// error would make the poller offer it again on every tick forever.
func TestARejectedInboundMessageIsDroppedWithoutAnError(t *testing.T) {
	screener := &stubScreener{decision: decisionOf(emailfilter.ActionReject, "known bad sender")}
	u, repo, trigger := harness(t, screener)

	if err := u.Process(context.Background(), inboundOf()); err != nil {
		t.Fatalf("Process returned %v, want nil so the poller stops offering it", err)
	}
	if len(repo.written) != 0 {
		t.Errorf("stored %d messages, want none", len(repo.written))
	}
	if trigger.enqueued != 0 {
		t.Errorf("webhook fired for a rejected message")
	}
	if len(screener.recorded) != 1 {
		t.Errorf("the rejection was not recorded")
	}
}

func TestATaggedInboundMessageIsDeliveredNormally(t *testing.T) {
	screener := &stubScreener{decision: decisionOf(emailfilter.ActionTag, "watch")}
	u, repo, trigger := harness(t, screener)

	if err := u.Process(context.Background(), inboundOf()); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(repo.written) != 1 || trigger.enqueued != 1 {
		t.Errorf("stored=%d webhooks=%d, want the message delivered normally", len(repo.written), trigger.enqueued)
	}
	if len(screener.recorded) != 1 {
		t.Errorf("the tag was not recorded")
	}
}

// Inbound fails open, which is the opposite of the send path and deliberate:
// the message has already been accepted over SMTP, so dropping it because a
// database was unreachable would lose mail outright.
func TestAnInboundScreeningFailureStillDelivers(t *testing.T) {
	screener := &stubScreener{screenErr: errors.New("database unreachable")}
	u, repo, trigger := harness(t, screener)

	if err := u.Process(context.Background(), inboundOf()); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(repo.written) != 1 || trigger.enqueued != 1 {
		t.Errorf("stored=%d webhooks=%d, want the message delivered despite the failure", len(repo.written), trigger.enqueued)
	}
}

func TestInboundHeadersReachTheRules(t *testing.T) {
	screener := &stubScreener{}
	u, _, _ := harness(t, screener)

	if err := u.Process(context.Background(), inboundOf()); err != nil {
		t.Fatalf("Process: %v", err)
	}
	// Lower-cased, because a rule writes X-Spam-Score and the wire may carry
	// any casing at all.
	if got := screener.seen.Header("x-spam-score"); len(got) != 1 || got[0] != "9.1" {
		t.Errorf("header = %v, want the inbound header available to rules", got)
	}
}

func TestWithoutAScreenerInboundIsUnchanged(t *testing.T) {
	u, repo, trigger := harness(t, nil)

	if err := u.Process(context.Background(), inboundOf()); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(repo.written) != 1 || trigger.enqueued != 1 {
		t.Errorf("stored=%d webhooks=%d, want unfiltered behaviour", len(repo.written), trigger.enqueued)
	}
}

// A hold suppresses the webhook rather than discarding the mail, so releasing
// one is firing the notification that was withheld.
func TestReleasingAHeldInboundMessageFiresTheWebhook(t *testing.T) {
	repo := &stubRepo{}
	trigger := &stubTrigger{}

	// The message is on disk, as a hold leaves it.
	if err := repo.Write(context.Background(), &entities.InboundEmail{
		ID: "msg-1", TenantID: "tenant-1", From: "sender@outside.example",
		To: []string{"support@example.com"}, Subject: "Invoice",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	repo.found = repo.written[0]

	releaser := NewHeldReleaser(repo, trigger)
	if err := releaser.ReleaseHeld(context.Background(), "tenant-1", "msg-1"); err != nil {
		t.Fatalf("ReleaseHeld: %v", err)
	}
	if trigger.enqueued != 1 {
		t.Errorf("webhook fired %d times, want 1", trigger.enqueued)
	}
}

// A reviewer told the message went out has to be right about that.
func TestReleasingAMessageThatIsNoLongerStoredIsAnError(t *testing.T) {
	releaser := NewHeldReleaser(&stubRepo{}, &stubTrigger{})
	if err := releaser.ReleaseHeld(context.Background(), "tenant-1", "gone"); err == nil {
		t.Fatal("ReleaseHeld reported success for a message that is not there")
	}
}

func (f *stubScreener) SetRetention(time.Duration) {}
