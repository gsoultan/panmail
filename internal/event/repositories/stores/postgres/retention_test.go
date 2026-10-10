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
		{"minute series", func(s *Store) (int64, error) { return s.PruneMinuteSeriesBefore(t.Context(), cutoff) }, "minute_series"},
		{"resource history", func(s *Store) (int64, error) { return s.PruneResourceHistoryBefore(t.Context(), cutoff) }, "resource_history"},
		{"analytics series", func(s *Store) (int64, error) { return s.PruneHourDaySeriesBefore(t.Context(), cutoff) }, "analytics_series"},
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

// Minute rows past the horizon go; day and hour rows stay for good, since a
// long chart is drawn from them and migration 0015 exists because losing them
// emptied it.
func TestMinutePruneKeepsDayAndHourRows(t *testing.T) {
	s, w := newStore(t)
	old := time.Now().UTC().AddDate(0, 0, -45).Truncate(time.Minute)
	now := time.Now().UTC()
	writeEvents(t, s, w,
		ev("old", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old),
		ev("new", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, now))

	removed, err := s.DeleteMinuteSeriesBefore(t.Context(), now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("DeleteMinuteSeriesBefore: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d rows; want the old minute", removed)
	}

	for _, tt := range []struct {
		granularity string
		bucket      string
		want        int64
	}{
		{"minute", old.Format("2006-01-02 15:04"), 0},
		{"minute", now.Format("2006-01-02 15:04"), 1},
		{"hour", old.Format("2006-01-02 15"), 1},
		{"day", old.Format("2006-01-02"), 1},
	} {
		got, err := s.GetTimeSeriesMetrics(t.Context(), storetest.TenantA, time.Time{}, time.Time{}, tt.granularity)
		if err != nil {
			t.Fatalf("%s series: %v", tt.granularity, err)
		}
		if got[tt.bucket]["SENT"] != tt.want {
			t.Errorf("%s %s = %d; want %d", tt.granularity, tt.bucket, got[tt.bucket]["SENT"], tt.want)
		}
	}
}

// With an analytics retention set, hour and day rows before it go and minute
// rows are left to their own horizon. A day row goes only once the whole day
// is past the cutoff, so the cutoff's own day keeps its count.
func TestHourDayPruneKeepsMinutesAndTheCutoffDay(t *testing.T) {
	s, w := newStore(t)
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -400).Truncate(time.Minute)
	writeEvents(t, s, w,
		ev("old", "a@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, old),
		ev("new", "b@example.com", panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, now))

	removed, err := s.DeleteHourDaySeriesBefore(t.Context(), now.AddDate(0, 0, -365))
	if err != nil {
		t.Fatalf("DeleteHourDaySeriesBefore: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed %d rows; want the old hour and the old day", removed)
	}

	for _, tt := range []struct {
		granularity string
		bucket      string
		want        int64
	}{
		{"hour", old.Format("2006-01-02 15"), 0},
		{"day", old.Format("2006-01-02"), 0},
		{"minute", old.Format("2006-01-02 15:04"), 1},
		{"hour", now.Format("2006-01-02 15"), 1},
		{"day", now.Format("2006-01-02"), 1},
	} {
		got, err := s.GetTimeSeriesMetrics(t.Context(), storetest.TenantA, time.Time{}, time.Time{}, tt.granularity)
		if err != nil {
			t.Fatalf("%s series: %v", tt.granularity, err)
		}
		if got[tt.bucket]["SENT"] != tt.want {
			t.Errorf("%s %s = %d; want %d", tt.granularity, tt.bucket, got[tt.bucket]["SENT"], tt.want)
		}
	}
}
