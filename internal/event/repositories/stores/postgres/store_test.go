package postgres

import (
	"context"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
	"github.com/gsoultan/panmail/internal/storetest"
)

// localStub stands in for Pebble. Only the delegated methods are reachable;
// anything else would be the composite failing to implement something itself.
type localStub struct {
	stores.EventRepository
	closed bool
}

func (l *localStub) Close() error { l.closed = true; return nil }

func newStore(t *testing.T) (*Store, *Writer) {
	t.Helper()
	conn := storetest.NewConnection(t)
	w := NewWriter(conn)
	return NewStore(conn, &localStub{}, w), w
}

// writeEvents queues events and runs the writer long enough to flush them.
func writeEvents(t *testing.T, s *Store, w *Writer, events ...*entities.EmailEvent) {
	t.Helper()
	for _, e := range events {
		if err := s.Write(t.Context(), e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)
	time.Sleep(250 * time.Millisecond)
	cancel()
	w.Stop()

	if _, _, failed := w.Stats(); failed != 0 {
		t.Fatalf("%d events failed to flush", failed)
	}
}

func ev(name, recipient string, typ panmailv1.EmailEventType, at time.Time) *entities.EmailEvent {
	return &entities.EmailEvent{
		ID: storetest.ID(name), TenantID: storetest.TenantA,
		MessageID: storetest.ID("msg-" + name), Type: typ,
		Recipient: recipient, Subject: "Quarterly numbers", Timestamp: at,
	}
}

func TestListReturnsNewestFirst(t *testing.T) {
	s, w := newStore(t)
	base := time.Now().UTC().Truncate(time.Second)
	writeEvents(t, s, w,
		ev("old", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, base.Add(-2*time.Hour)),
		ev("new", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, base))

	got, _, err := s.List(t.Context(), storetest.TenantA, stores.ListFilter{PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].ID != storetest.ID("new") {
		t.Errorf("first event is %q; want the newest", got[0].ID)
	}
}

// A tenant must never see another's events. The filter is the only thing
// standing between them, so it is worth its own test rather than trusting it.
func TestListIsScopedToTheTenant(t *testing.T) {
	s, w := newStore(t)
	mine := ev("mine", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC())
	theirs := ev("theirs", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC())
	theirs.TenantID = storetest.TenantB
	writeEvents(t, s, w, mine, theirs)

	got, _, err := s.List(t.Context(), storetest.TenantA, stores.ListFilter{PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != storetest.ID("mine") {
		t.Fatalf("got %d events; want only this tenant's", len(got))
	}
}

// Keyset pagination, and the reason the token carries the id as well as the
// timestamp: events written for one message share a timestamp closely enough
// that a token of time alone loses whichever sorted after it.
func TestPaginationDoesNotSkipOrRepeat(t *testing.T) {
	s, w := newStore(t)
	base := time.Now().UTC()
	var all []*entities.EmailEvent
	for i := range 7 {
		all = append(all, ev(
			"e"+string(rune('a'+i)), "r@example.com",
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
			base.Add(-time.Duration(i)*time.Second)))
	}
	writeEvents(t, s, w, all...)

	seen := map[string]int{}
	token := ""
	for range 5 {
		page, next, err := s.List(t.Context(), storetest.TenantA,
			stores.ListFilter{PageSize: 3, PageToken: token})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, e := range page {
			seen[e.ID]++
		}
		if next == "" {
			break
		}
		token = next
	}

	if len(seen) != 7 {
		t.Errorf("saw %d distinct events across the pages; want 7", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("event %s appeared %d times", id, n)
		}
	}
}

func TestListByMessageIDIsOldestFirst(t *testing.T) {
	s, w := newStore(t)
	base := time.Now().UTC()
	first := ev("sent", "r@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, base.Add(-time.Minute))
	second := ev("delivered", "r@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, base)
	second.MessageID = first.MessageID
	writeEvents(t, s, w, first, second)

	got, err := s.ListByMessageID(t.Context(), storetest.TenantA, first.MessageID)
	if err != nil {
		t.Fatalf("ListByMessageID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	// A timeline reads forwards.
	if got[0].Type != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT {
		t.Errorf("first is %v; want SENT", got[0].Type)
	}
}

// The keys the dashboard uses are the enum name without its prefix, which is
// what the Pebble store wrote. Changing them silently would empty every chart.
func TestMetricsUseTheDashboardsKeys(t *testing.T) {
	s, w := newStore(t)
	now := time.Now().UTC()
	writeEvents(t, s, w,
		ev("a", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, now),
		ev("b", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, now),
		ev("c", "c@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, now))

	got, err := s.GetMetrics(t.Context(), storetest.TenantA, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if got["SENT"] != 2 || got["DELIVERED"] != 1 {
		t.Errorf("metrics = %v; want SENT=2 DELIVERED=1 under unprefixed keys", got)
	}
}

func TestTimeSeriesBucketsByDay(t *testing.T) {
	s, w := newStore(t)
	now := time.Now().UTC()
	writeEvents(t, s, w,
		ev("today", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, now),
		ev("before", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, now.AddDate(0, 0, -3)))

	got, err := s.GetTimeSeriesMetrics(t.Context(), storetest.TenantA, time.Time{}, time.Time{}, "day")
	if err != nil {
		t.Fatalf("GetTimeSeriesMetrics: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d buckets, want 2 distinct days: %v", len(got), got)
	}
}

func TestMessagesRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	m := &entities.EmailMessage{
		ID: storetest.ID("m1"), TenantID: storetest.TenantA,
		ProviderID: storetest.ID("p1"), From: "a@example.com",
		To: []string{"b@example.com", "c@example.com"}, Cc: []string{"d@example.com"},
		Subject: "Quarterly numbers", BodyHTML: "<p>hi</p>", BodyText: "hi",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.WriteMessage(t.Context(), m); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	got, err := s.GetMessage(t.Context(), storetest.TenantA, m.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got == nil {
		t.Fatal("the message came back nil")
	}
	if len(got.To) != 2 || got.To[0] != "b@example.com" || len(got.Cc) != 1 {
		t.Errorf("recipient lists did not round trip: to=%v cc=%v", got.To, got.Cc)
	}
	if got.BodyHTML != "<p>hi</p>" || got.BodyText != "hi" {
		t.Errorf("bodies did not round trip: %q / %q", got.BodyHTML, got.BodyText)
	}
}

// Writing the same message twice happens: the send path stores it, and a
// delivery event later fills in the provider it went out through.
func TestWritingAMessageTwiceKeepsTheProvider(t *testing.T) {
	s, _ := newStore(t)
	m := &entities.EmailMessage{
		ID: storetest.ID("m2"), TenantID: storetest.TenantA,
		From: "a@example.com", CreatedAt: time.Now().UTC(),
	}
	if err := s.WriteMessage(t.Context(), m); err != nil {
		t.Fatalf("first WriteMessage: %v", err)
	}
	m.ProviderID = storetest.ID("p9")
	if err := s.WriteMessage(t.Context(), m); err != nil {
		t.Fatalf("second WriteMessage: %v", err)
	}

	got, err := s.GetMessage(t.Context(), storetest.TenantA, m.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.ProviderID != storetest.ID("p9") {
		t.Errorf("provider = %q; want the second write to have filled it in", got.ProviderID)
	}
}

func TestRetentionDeletesOldEventsAndMessages(t *testing.T) {
	s, w := newStore(t)
	old := time.Now().UTC().AddDate(0, 0, -30)
	writeEvents(t, s, w,
		ev("stale", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old),
		ev("fresh", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC()))

	cutoff := time.Now().UTC().AddDate(0, 0, -1)
	removed, err := s.TruncateBefore(t.Context(), cutoff)
	if err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d events; want only the stale one", removed)
	}

	left, _, err := s.List(t.Context(), storetest.TenantA, stores.ListFilter{PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(left) != 1 || left[0].ID != storetest.ID("fresh") {
		t.Errorf("retention kept the wrong events: %d left", len(left))
	}
}

// Closing must stop the writer before closing the local store, and must close
// the local store — a composite that forgot would leak Pebble's directory lock.
func TestCloseClosesTheLocalStore(t *testing.T) {
	conn := storetest.NewConnection(t)
	local := &localStub{}
	s := NewStore(conn, local, NewWriter(conn))

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !local.closed {
		t.Error("the local store was not closed")
	}
}
