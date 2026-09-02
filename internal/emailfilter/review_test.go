package emailfilter_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/emailfilter"
)

// fakeQuarantine models the one property the real store guarantees: Review is
// a conditional update that only touches a PENDING row, so exactly one of two
// concurrent reviewers wins.
type fakeQuarantine struct {
	mu      sync.Mutex
	records map[string]*emailfilter.FilteredMessage
}

func newFakeQuarantine(records ...*emailfilter.FilteredMessage) *fakeQuarantine {
	q := &fakeQuarantine{records: map[string]*emailfilter.FilteredMessage{}}
	for _, r := range records {
		q.records[r.ID] = r
	}
	return q
}

func (q *fakeQuarantine) Create(context.Context, *emailfilter.FilteredMessage) error { return nil }

func (q *fakeQuarantine) Get(_ context.Context, _, id string) (*emailfilter.FilteredMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.records[id]
	if !ok {
		return nil, emailfilter.ErrNotFound
	}
	copied := *r
	return &copied, nil
}

func (q *fakeQuarantine) List(context.Context, string, emailfilter.QuarantineFilter) ([]emailfilter.FilteredMessage, string, error) {
	return nil, "", nil
}

func (q *fakeQuarantine) Review(_ context.Context, _, id string, status emailfilter.Status, by, note string) (*emailfilter.FilteredMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.records[id]
	if !ok {
		return nil, emailfilter.ErrNotFound
	}
	if r.Status != emailfilter.StatusPending {
		return nil, emailfilter.ErrAlreadyReviewed
	}
	r.Status, r.ReviewedBy, r.ReviewNote = status, by, note
	now := time.Now()
	r.ReviewedAt = &now
	copied := *r
	return &copied, nil
}

func (q *fakeQuarantine) Expire(context.Context, time.Time, int) (int, error) { return 0, nil }

type countingReleaser struct {
	mu        sync.Mutex
	released  int
	discarded int
	err       error
}

func (c *countingReleaser) ReleaseHeld(context.Context, string, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.released++
	return c.err
}

func (c *countingReleaser) DiscardHeld(context.Context, string, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.discarded++
	return nil
}

func heldRecord() *emailfilter.FilteredMessage {
	return &emailfilter.FilteredMessage{
		ID: "q1", TenantID: "t1", Direction: emailfilter.DirectionOutbound,
		Action: emailfilter.ActionHold, Status: emailfilter.StatusPending,
		RuleName: "big attachments", MessageID: "m1", PayloadRef: "m1",
	}
}

func TestReleaseSendsTheMessageAndMarksIt(t *testing.T) {
	q := newFakeQuarantine(heldRecord())
	out := &countingReleaser{}
	r := emailfilter.NewReviewer(q, out, nil)

	record, err := r.Release(context.Background(), "t1", "q1", "alice", "looks fine")
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if record.Status != emailfilter.StatusReleased {
		t.Errorf("status = %q, want RELEASED", record.Status)
	}
	if out.released != 1 {
		t.Errorf("released %d times, want 1", out.released)
	}
}

// The one outcome a review queue must never produce. Two reviewers pressing
// release at the same moment is not a rare race — it is what happens whenever
// a queue is worked by more than one person.
func TestConcurrentReleasesSendTheMessageOnce(t *testing.T) {
	q := newFakeQuarantine(heldRecord())
	out := &countingReleaser{}
	r := emailfilter.NewReviewer(q, out, nil)

	const reviewers = 8
	var wg sync.WaitGroup
	errs := make([]error, reviewers)
	wg.Add(reviewers)
	for i := 0; i < reviewers; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = r.Release(context.Background(), "t1", "q1", "reviewer", "")
		}(i)
	}
	wg.Wait()

	if out.released != 1 {
		t.Fatalf("the message was released %d times, want exactly 1", out.released)
	}
	var won, lost int
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, emailfilter.ErrAlreadyReviewed):
			lost++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 || lost != reviewers-1 {
		t.Errorf("won=%d lost=%d, want exactly one winner", won, lost)
	}
}

// The cost of claiming the status first. Visible, loud, and recoverable —
// where a double send cannot be taken back.
func TestAFailedDeliveryStillMarksTheMessageAndReportsIt(t *testing.T) {
	q := newFakeQuarantine(heldRecord())
	out := &countingReleaser{err: errors.New("outbox unreachable")}
	r := emailfilter.NewReviewer(q, out, nil)

	record, err := r.Release(context.Background(), "t1", "q1", "alice", "")
	if err == nil {
		t.Fatal("Release reported success when the message did not go")
	}
	if record == nil || record.Status != emailfilter.StatusReleased {
		t.Errorf("the decision was not recorded: %+v", record)
	}
}

func TestRejectDiscardsThePayload(t *testing.T) {
	q := newFakeQuarantine(heldRecord())
	out := &countingReleaser{}
	r := emailfilter.NewReviewer(q, out, nil)

	record, err := r.Reject(context.Background(), "t1", "q1", "alice", "phishing")
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if record.Status != emailfilter.StatusRejected {
		t.Errorf("status = %q, want REJECTED", record.Status)
	}
	if out.discarded != 1 {
		t.Errorf("discarded %d times, want 1", out.discarded)
	}
	if out.released != 0 {
		t.Error("a rejected message was released")
	}
}

func TestASecondDecisionIsRefused(t *testing.T) {
	q := newFakeQuarantine(heldRecord())
	r := emailfilter.NewReviewer(q, &countingReleaser{}, nil)

	if _, err := r.Release(context.Background(), "t1", "q1", "alice", ""); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := r.Reject(context.Background(), "t1", "q1", "bob", ""); !errors.Is(err, emailfilter.ErrAlreadyReviewed) {
		t.Errorf("err = %v, want ErrAlreadyReviewed", err)
	}
}

// A reviewer told a message went out has to be right about that.
func TestReleasingWithoutAConfiguredReleaserIsAnError(t *testing.T) {
	q := newFakeQuarantine(heldRecord())
	r := emailfilter.NewReviewer(q, nil, nil)

	if _, err := r.Release(context.Background(), "t1", "q1", "alice", ""); err == nil {
		t.Fatal("Release claimed success with no way to deliver")
	}
}

func TestUnknownIdIsNotFound(t *testing.T) {
	r := emailfilter.NewReviewer(newFakeQuarantine(), &countingReleaser{}, nil)
	if _, err := r.Release(context.Background(), "t1", "missing", "alice", ""); !errors.Is(err, emailfilter.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// countingNotifier records what a hold announced.
type countingNotifier struct {
	events []emailfilter.HeldEvent
}

func (c *countingNotifier) NotifyHeld(_ string, e emailfilter.HeldEvent) {
	c.events = append(c.events, e)
}

func heldMessage() emailfilter.Message {
	return emailfilter.Message{
		From: "alice@example.com", To: []string{"bob@partner.net"},
		Subject: "Quarterly numbers", Size: 5_000_000,
		Attachments: []emailfilter.Attachment{{Filename: "numbers.xlsx", Size: 4_000_000}},
	}
}

func holdDecision() emailfilter.Decision {
	return emailfilter.Decision{
		Action: emailfilter.ActionHold,
		Rule:   &emailfilter.Rule{ID: "r1", Name: "large attachments", Action: emailfilter.ActionHold},
		Matched: []emailfilter.Condition{
			{Field: emailfilter.FieldHasAttachment, Operator: emailfilter.OpIsTrue},
		},
	}
}

// Without this a hold is silent: nothing tells anyone, nobody opens the queue,
// and retention expires the message unreviewed.
func TestAHeldMessageNotifiesTheTenant(t *testing.T) {
	notifier := &countingNotifier{}
	s := emailfilter.NewScreener(nil, newFakeQuarantine(), 0, notifier)

	record := emailfilter.NewFilteredMessage("t1", emailfilter.DirectionOutbound, heldMessage(), holdDecision())
	if err := s.Record(context.Background(), &record); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if len(notifier.events) != 1 {
		t.Fatalf("notified %d times, want 1", len(notifier.events))
	}
	event := notifier.events[0]
	if event.RuleName != "large attachments" {
		t.Errorf("rule = %q, want it named so a subscriber can triage", event.RuleName)
	}
	if len(event.Matched) != 1 || event.Matched[0].Field != "has_attachment" {
		t.Errorf("matched = %+v, want the reason carried", event.Matched)
	}
	// The id is what the review API takes, so a subscriber can act on the
	// notification rather than go hunting in the dashboard.
	if event.ID != record.ID {
		t.Errorf("id = %q, want the review-queue id %q", event.ID, record.ID)
	}
	// An expiry a subscriber can escalate before, rather than discover after.
	if event.ExpiresAt == nil {
		t.Error("no expiry was sent; a subscriber cannot tell how long it has")
	}
}

// A tag was delivered and a reject is already decided. Neither is waiting on a
// person, so neither should wake one.
func TestOnlyAHoldNotifies(t *testing.T) {
	for _, action := range []emailfilter.Action{emailfilter.ActionTag, emailfilter.ActionReject} {
		notifier := &countingNotifier{}
		s := emailfilter.NewScreener(nil, newFakeQuarantine(), 0, notifier)

		decision := holdDecision()
		decision.Action = action
		record := emailfilter.NewFilteredMessage("t1", emailfilter.DirectionOutbound, heldMessage(), decision)
		if err := s.Record(context.Background(), &record); err != nil {
			t.Fatalf("Record: %v", err)
		}
		if len(notifier.events) != 0 {
			t.Errorf("%s notified %d times, want 0", action, len(notifier.events))
		}
	}
}

// A deployment with no webhook worker must still be able to filter.
func TestHoldingWithoutANotifierStillWorks(t *testing.T) {
	s := emailfilter.NewScreener(nil, newFakeQuarantine(), 0, nil)

	record := emailfilter.NewFilteredMessage("t1", emailfilter.DirectionOutbound, heldMessage(), holdDecision())
	if err := s.Record(context.Background(), &record); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

// The payload is a public contract, so it is pinned rather than left to fall
// out of whatever the domain struct happens to be called this month.
func TestTheHeldEventIsStableJSON(t *testing.T) {
	notifier := &countingNotifier{}
	s := emailfilter.NewScreener(nil, newFakeQuarantine(), 0, notifier)

	record := emailfilter.NewFilteredMessage("t1", emailfilter.DirectionOutbound, heldMessage(), holdDecision())
	record.ID = "q1"
	if err := s.Record(context.Background(), &record); err != nil {
		t.Fatalf("Record: %v", err)
	}

	encoded, err := json.Marshal(notifier.events[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, field := range []string{
		"id", "direction", "rule_name", "from", "recipients", "subject",
		"size_bytes", "attachment_count", "matched", "held_at", "expires_at",
	} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("the payload lost %q, which is a breaking change for subscribers", field)
		}
	}

	// No body and no attachment content: a webhook endpoint is a URL a tenant
	// typed once, and the message itself is behind the authenticated review API.
	for _, leaked := range []string{"html", "text", "body", "content", "attachments"} {
		if _, ok := decoded[leaked]; ok {
			t.Errorf("the payload carries %q; a held message's content must stay behind the review API", leaked)
		}
	}
}
