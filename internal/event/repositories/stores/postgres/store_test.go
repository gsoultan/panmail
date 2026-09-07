package postgres

import (
	"context"
	"errors"
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
	closed     bool
	archived   []*entities.EmailEvent
	archiveErr error
	written    []*entities.EmailEvent
	messages   []*entities.EmailMessage
}

// Write and WriteMessage exist so the tests can tell whether the shared store
// is still mirroring to the local one, which is what makes a revert a restart.
func (l *localStub) Write(_ context.Context, e *entities.EmailEvent) error {
	l.written = append(l.written, e)
	return nil
}

func (l *localStub) WriteMessage(_ context.Context, m *entities.EmailMessage) error {
	l.messages = append(l.messages, m)
	return nil
}

// ArchiveEvents is what the shared store calls before deleting. Recording it
// here is how the test below can tell archiving from deleting.
func (l *localStub) ArchiveEvents(_ context.Context, events []*entities.EmailEvent) error {
	if l.archiveErr != nil {
		return l.archiveErr
	}
	l.archived = append(l.archived, events...)
	return nil
}

func (l *localStub) Close() error { l.closed = true; return nil }

func newStore(t *testing.T) (*Store, *Writer) {
	t.Helper()
	s, w, _ := newStoreWithLocal(t)
	return s, w
}

func newStoreWithLocal(t *testing.T) (*Store, *Writer, *localStub) {
	t.Helper()
	conn := storetest.NewConnection(t)
	w := NewWriter(conn)
	local := &localStub{}
	return NewStore(conn, local, w), w, local
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

// The all-time figures come from the counters, and the counters are lifetime
// totals. Retention prunes events and must leave them alone — the Pebble store
// this replaces never touched its metrics keys during a prune, so "Emails Sent"
// means how many were ever sent, not how many rows survive.
//
// count(*) would have dropped here, and the headline figure falling after a
// retention pass is both wrong and alarming.
func TestLifetimeCountersSurviveRetention(t *testing.T) {
	s, w := newStore(t)
	old := time.Now().UTC().AddDate(0, 0, -30)
	writeEvents(t, s, w,
		ev("stale", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old),
		ev("fresh", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC()))

	before, err := s.GetMetrics(t.Context(), storetest.TenantA, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if before["SENT"] != 2 {
		t.Fatalf("SENT = %d before retention; want 2", before["SENT"])
	}

	if _, err := s.TruncateBefore(t.Context(), time.Now().UTC().AddDate(0, 0, -1)); err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}

	after, err := s.GetMetrics(t.Context(), storetest.TenantA, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if after["SENT"] != 2 {
		t.Errorf("SENT = %d after retention pruned one event; want the lifetime total to hold at 2",
			after["SENT"])
	}
}

// A window still counts rows, because a window is a question about what
// happened in it — and after retention, what happened in a pruned window is
// nothing. That is the difference from the lifetime figure, and it is
// deliberate rather than an inconsistency.
func TestAWindowCountsRowsRatherThanTheCounter(t *testing.T) {
	s, w := newStore(t)
	now := time.Now().UTC()
	writeEvents(t, s, w,
		ev("old", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, now.AddDate(0, 0, -10)),
		ev("new", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, now))

	got, err := s.GetMetrics(t.Context(), storetest.TenantA, now.AddDate(0, 0, -1), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if got["SENT"] != 1 {
		t.Errorf("SENT in the last day = %d; want 1, not the lifetime 2", got["SENT"])
	}
}

// Counters are per tenant. One tenant's traffic must never appear in another's
// headline figures, and a counter table makes that a primary key rather than a
// filter somebody could forget.
func TestCountersAreScopedToTheTenant(t *testing.T) {
	s, w := newStore(t)
	mine := ev("mine", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC())
	theirs := ev("theirs", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC())
	theirs.TenantID = storetest.TenantB
	writeEvents(t, s, w, mine, theirs)

	got, err := s.GetMetrics(t.Context(), storetest.TenantA, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if got["SENT"] != 1 {
		t.Errorf("SENT = %d; want only this tenant's event", got["SENT"])
	}
}

// Retention must archive before it deletes.
//
// app.archive_retention_days is documented as the escape hatch for
// log_retention_days, which defaults to fourteen days rather than to forever —
// so a bare DELETE here loses delivery history two weeks after a deployment
// switches to the shared store, silently, and nothing recovers it.
//
// The Pebble store this replaced wrote every expiring event to a per-tenant
// JSONL file first. This is that behaviour, kept.
func TestRetentionArchivesBeforeDeleting(t *testing.T) {
	s, w, local := newStoreWithLocal(t)
	old := time.Now().UTC().AddDate(0, 0, -30)
	writeEvents(t, s, w,
		ev("stale-a", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old),
		ev("stale-b", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, old),
		ev("fresh", "c@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC()))

	removed, err := s.TruncateBefore(t.Context(), time.Now().UTC().AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed %d events; want the two stale ones", removed)
	}
	if len(local.archived) != 2 {
		t.Fatalf("archived %d events; want every deleted event archived first", len(local.archived))
	}

	// The archived records must carry enough to be worth keeping.
	if local.archived[0].Recipient == "" || local.archived[0].TenantID == "" {
		t.Errorf("archived record is missing fields: %+v", local.archived[0])
	}

	left, _, err := s.List(t.Context(), storetest.TenantA, stores.ListFilter{PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(left) != 1 {
		t.Errorf("%d events left; want only the fresh one", len(left))
	}
}

// A failed archive must stop the pass rather than delete anyway.
//
// Archiving a row that is then not deleted costs a duplicate entry next pass.
// Deleting a row that was not archived loses it, and nothing recovers from
// that — so the asymmetry decides which way this fails.
func TestAFailedArchiveDoesNotDelete(t *testing.T) {
	s, w, local := newStoreWithLocal(t)
	local.archiveErr = errors.New("the archive volume is full")

	old := time.Now().UTC().AddDate(0, 0, -30)
	writeEvents(t, s, w, ev("stale", "a@example.com",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old))

	if _, err := s.TruncateBefore(t.Context(), time.Now().UTC().AddDate(0, 0, -1)); err == nil {
		t.Fatal("a failed archive reported success")
	}

	left, _, err := s.List(t.Context(), storetest.TenantA, stores.ListFilter{PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(left) != 1 {
		t.Errorf("%d events left; the event was deleted despite the archive failing", len(left))
	}
}

// Switching reads must not stop the local writes. That distinction is the
// whole safety story for step three: while the local store is still written,
// reverting to it is a restart, because it has everything.
//
// The first version of this store lost that by writing only to the database as
// soon as reads moved — which was documented as reversible and was not.
func TestSharedModeStillMirrorsToTheLocalStore(t *testing.T) {
	conn := storetest.NewConnection(t)
	w := NewWriter(conn)
	local := &localStub{}
	s := NewStore(conn, local, w)

	if err := s.Write(t.Context(), ev("mirrored", "a@example.com",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC())); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(local.written) != 1 {
		t.Errorf("the local store received %d events; want the write mirrored so a revert is a restart",
			len(local.written))
	}

	m := &entities.EmailMessage{ID: storetest.ID("m"), TenantID: storetest.TenantA,
		From: "a@example.com", CreatedAt: time.Now().UTC()}
	if err := s.WriteMessage(t.Context(), m); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if len(local.messages) != 1 {
		t.Errorf("the local store received %d messages; want the write mirrored", len(local.messages))
	}
}

// And the last step does stop them, which is what makes it the one a restart
// cannot undo.
func TestWithoutLocalWritesStopsMirroring(t *testing.T) {
	conn := storetest.NewConnection(t)
	local := &localStub{}
	s := NewStore(conn, local, NewWriter(conn)).WithoutLocalWrites()

	if err := s.Write(t.Context(), ev("only-shared", "a@example.com",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC())); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(local.written) != 0 {
		t.Errorf("the local store received %d events after the writes were stopped", len(local.written))
	}
}
