package postgres

import (
	"context"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/storetest"
)

func newWriter(t *testing.T) *Writer {
	t.Helper()
	return NewWriter(storetest.NewConnection(t))
}

func event(t *testing.T, name, recipient string) *entities.EmailEvent {
	t.Helper()
	return &entities.EmailEvent{
		ID:         storetest.ID(name),
		TenantID:   storetest.TenantA,
		ProviderID: storetest.ID("provider"),
		MessageID:  storetest.ID("msg-" + name),
		Type:       panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
		Recipient:  recipient,
		Subject:    "a subject",
		Timestamp:  time.Now().UTC(),
	}
}

// flushNow runs the loop just long enough to drain what was queued.
func flushNow(t *testing.T, w *Writer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)
	time.Sleep(250 * time.Millisecond)
	cancel()
	w.Stop()
}

func TestEventsReachTheSharedStore(t *testing.T) {
	w := newWriter(t)
	w.Write(event(t, "a", "a@example.com"))
	w.Write(event(t, "b", "b@example.com"))
	flushNow(t, w)

	written, dropped, failed := w.Stats()
	if written != 2 || dropped != 0 || failed != 0 {
		t.Fatalf("written=%d dropped=%d failed=%d; want 2/0/0", written, dropped, failed)
	}

	var count int
	if err := w.conn.GetDB().QueryRow(`SELECT count(*) FROM email_events`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("%d rows in email_events; want 2", count)
	}
}

// An event recorded before a provider is chosen genuinely has none — a DROPPED
// event on a suppressed recipient is the common case. The empty string is not a
// UUID, and PostgreSQL rejects it outright, so it has to become NULL.
func TestAnEventWithNoProviderIsStored(t *testing.T) {
	w := newWriter(t)
	e := event(t, "no-provider", "c@example.com")
	e.ProviderID = ""
	e.Type = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DROPPED

	w.Write(e)
	flushNow(t, w)

	if written, _, failed := w.Stats(); written != 1 || failed != 0 {
		t.Fatalf("written=%d failed=%d; want 1/0 — an absent provider must not fail the batch",
			written, failed)
	}
}

// The type is stored as the enum's name rather than its number, so a row
// written by a build that knows a type this one does not reads as an unknown
// string instead of silently matching some other type.
func TestTheEventTypeIsStoredByName(t *testing.T) {
	w := newWriter(t)
	w.Write(event(t, "typed", "d@example.com"))
	flushNow(t, w)

	var got string
	if err := w.conn.GetDB().QueryRow(`SELECT type FROM email_events LIMIT 1`).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != "EMAIL_EVENT_TYPE_DELIVERED" {
		t.Errorf("type = %q; want the enum name", got)
	}
}

// Metadata is the least valuable field on the row, so a map that will not
// marshal must not take the other events in the batch down with it.
func TestUnmarshallableMetadataDoesNotFailTheBatch(t *testing.T) {
	w := newWriter(t)
	bad := event(t, "bad-meta", "e@example.com")
	bad.Metadata = map[string]any{"fn": func() {}} // channels and funcs do not marshal
	w.Write(bad)
	w.Write(event(t, "good", "f@example.com"))
	flushNow(t, w)

	if written, _, failed := w.Stats(); written != 2 || failed != 0 {
		t.Fatalf("written=%d failed=%d; want both rows stored", written, failed)
	}
}

// The send path must not be able to block on this. A full buffer drops and
// counts rather than waiting for the database.
func TestAFullQueueDropsRatherThanBlocking(t *testing.T) {
	w := newWriter(t) // not started, so nothing drains the channel

	for i := range queueDepth + 100 {
		w.Write(event(t, "flood", "g@example.com"))
		_ = i
	}

	if _, dropped, _ := w.Stats(); dropped == 0 {
		t.Error("a full queue blocked instead of dropping")
	}
}
