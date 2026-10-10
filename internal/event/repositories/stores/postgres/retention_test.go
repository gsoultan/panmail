package postgres

import (
	"slices"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
	"github.com/gsoultan/panmail/internal/storetest"
)

// In shared mode the local store is still written — every event and body,
// unless -stop-local-events is set — so retention has to reach it as well.
// Pruning only the database left events.db growing for the life of the
// deployment, whatever the policy said.
func TestSharedRetentionPrunesTheLocalStoreToo(t *testing.T) {
	cutoff := time.Now().UTC().AddDate(0, 0, -1)
	tests := []struct {
		name  string
		prune func(*Store) (int64, error)
		want  string
	}{
		{"events", func(s *Store) (int64, error) { return s.TruncateBefore(t.Context(), cutoff) }, "events"},
		{"messages", func(s *Store) (int64, error) { return s.TruncateMessagesBefore(t.Context(), cutoff) }, "messages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, local := newStoreWithLocal(t)
			if _, err := tt.prune(s); err != nil {
				t.Fatalf("prune: %v", err)
			}
			if !slices.Contains(local.pruned, tt.want) {
				t.Errorf("local store pruned %v; want %s pruned there too", local.pruned, tt.want)
			}
		})
	}
}

// Each expired event reaches the archive once. The local store holds the same
// events as the database, so archiving from both wrote every one twice; it
// also holds events the database never had — those written before the
// database was — and archiving from the database alone would lose those.
//
// An id that is not a uuid is answered as "not held" rather than sent: on
// PostgreSQL one malformed id in the IN list fails the whole query, and with
// it the prune.
func TestEachExpiredEventIsArchivedOnce(t *testing.T) {
	s, w, local := newStoreWithLocal(t)
	old := time.Now().UTC().AddDate(0, 0, -30)
	shared := ev("shared", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old)
	writeEvents(t, s, w, shared)

	localOnly := ev("local-only", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old)
	legacy := ev("legacy", "c@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old)
	legacy.ID = "not-a-uuid"
	local.expiring = []*entities.EmailEvent{shared, localOnly, legacy}

	removed, err := s.TruncateBefore(t.Context(), time.Now().UTC().AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d; want the database's count, one", removed)
	}

	count := map[string]int{}
	for _, e := range local.archived {
		count[e.ID]++
	}
	for _, id := range []string{shared.ID, localOnly.ID, legacy.ID} {
		if count[id] != 1 {
			t.Errorf("%s archived %d times; want once", id, count[id])
		}
	}
}

// The shadow phase's prune deletes the database's copy without archiving it:
// the primary archives the same events itself.
func TestDeleteEventsBeforeDoesNotArchive(t *testing.T) {
	s, w, local := newStoreWithLocal(t)
	old := time.Now().UTC().AddDate(0, 0, -30)
	writeEvents(t, s, w,
		ev("stale", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old),
		ev("fresh", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC()))

	removed, err := s.DeleteEventsBefore(t.Context(), time.Now().UTC().AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("DeleteEventsBefore: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d; want the stale one", removed)
	}
	if len(local.archived) != 0 || len(local.pruned) != 0 {
		t.Errorf("archived %d and pruned %v locally; want the database alone touched",
			len(local.archived), local.pruned)
	}
	left, _, err := s.List(t.Context(), storetest.TenantA, stores.ListFilter{PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(left) != 1 || left[0].ID != storetest.ID("fresh") {
		t.Errorf("%d events left; want only the fresh one", len(left))
	}
}
