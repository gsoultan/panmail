package pebble

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// Minute counters older than the horizon go; day and hour counters, and the
// lifetime totals, stay whatever their age. Several tenants, so the per-tenant
// seek past live minutes is exercised rather than one long run.
func TestPruneMinuteSeriesKeepsDaysAndHours(t *testing.T) {
	s := newRetentionStore(t)
	st := s.(*store)

	now := time.Now().UTC()
	old := now.AddDate(0, 0, -45)
	var events []*entities.EmailEvent
	for _, tenant := range []string{"tenant-a", "tenant-b", "tenant-c"} {
		for i, at := range []time.Time{old, old.Add(time.Minute), now} {
			events = append(events, &entities.EmailEvent{
				ID: fmt.Sprintf("%s-%d", tenant, i), TenantID: tenant, Timestamp: at,
				Type: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
			})
		}
	}
	for _, e := range events {
		if err := s.Write(t.Context(), e); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	waitForCount(t, st, "timeseries:minute:", 9)

	removed, err := s.PruneMinuteSeriesBefore(t.Context(), now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("PruneMinuteSeriesBefore: %v", err)
	}
	if removed != 6 {
		t.Errorf("removed %d minute counters; want the two old minutes of each of three tenants", removed)
	}

	if got := len(scanKeys(t, st.db, "timeseries:minute:")); got != 3 {
		t.Errorf("%d minute counters left; want this minute's, one per tenant", got)
	}
	if got := len(scanKeys(t, st.db, "timeseries:hour:")); got != 6 {
		t.Errorf("%d hour counters left; want every one kept", got)
	}
	metrics, err := s.GetMetrics(t.Context(), "tenant-a", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if metrics["SENT"] != 3 {
		t.Errorf("lifetime SENT = %d; want 3, untouched by the prune", metrics["SENT"])
	}
	days, err := s.GetTimeSeriesMetrics(t.Context(), "tenant-a", old, old, "day")
	if err != nil {
		t.Fatalf("day series: %v", err)
	}
	if days[old.Format(dayLayout)]["SENT"] != 2 {
		t.Errorf("day counters = %v; want the old day kept with both events", days)
	}
}

// With an analytics retention set, hour and day counters before it go; minute
// counters and lifetime totals are not this prune's to touch, and the day the
// cutoff falls in keeps its counter. Several tenants, so the per-tenant seek
// is exercised, and the day walk has to step over the hour and minute
// families that share its prefix.
func TestPruneHourDaySeries(t *testing.T) {
	s := newRetentionStore(t)
	st := s.(*store)

	now := time.Now().UTC()
	old := now.AddDate(0, 0, -400)
	var events []*entities.EmailEvent
	for _, tenant := range []string{"tenant-a", "tenant-b", "tenant-c"} {
		for i, at := range []time.Time{old, old.Add(time.Hour), now} {
			events = append(events, &entities.EmailEvent{
				ID: fmt.Sprintf("%s-%d", tenant, i), TenantID: tenant, Timestamp: at,
				Type: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
			})
		}
	}
	for _, e := range events {
		if err := s.Write(t.Context(), e); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	waitForCount(t, st, "timeseries:minute:", 9)
	countDays := func() int {
		n := 0
		for _, k := range scanKeys(t, st.db, "timeseries:") {
			if !strings.HasPrefix(k, "timeseries:hour:") && !strings.HasPrefix(k, "timeseries:minute:") {
				n++
			}
		}
		return n
	}
	// The old day and today, unless the two old events straddle midnight.
	daysBefore := countDays()

	cutoff := now.AddDate(0, 0, -365)
	removed, err := s.PruneHourDaySeriesBefore(t.Context(), cutoff)
	if err != nil {
		t.Fatalf("PruneHourDaySeriesBefore: %v", err)
	}
	// Two old hours per tenant, and every old day.
	if want := int64(6 + daysBefore - 3); removed != want {
		t.Errorf("removed %d counters; want %d", removed, want)
	}

	if got := len(scanKeys(t, st.db, "timeseries:hour:")); got != 3 {
		t.Errorf("%d hour counters left; want this hour's, one per tenant", got)
	}
	if got := countDays(); got != 3 {
		t.Errorf("%d day counters left; want today's, one per tenant", got)
	}
	if got := len(scanKeys(t, st.db, "timeseries:minute:")); got != 9 {
		t.Errorf("%d minute counters left; want every one kept", got)
	}
	metrics, err := s.GetMetrics(t.Context(), "tenant-a", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if metrics["SENT"] != 3 {
		t.Errorf("lifetime SENT = %d; want 3, untouched by the prune", metrics["SENT"])
	}

	// The day the cutoff falls in is kept, though part of it is before.
	edge := cutoff.Truncate(24 * time.Hour).Add(time.Minute)
	if err := s.Write(t.Context(), &entities.EmailEvent{
		ID: "edge", TenantID: "tenant-a", Timestamp: edge,
		Type: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForCount(t, st, "timeseries:minute:", 10)
	if _, err := s.PruneHourDaySeriesBefore(t.Context(), cutoff); err != nil {
		t.Fatalf("PruneHourDaySeriesBefore: %v", err)
	}
	days, err := s.GetTimeSeriesMetrics(t.Context(), "tenant-a", edge, edge, "day")
	if err != nil {
		t.Fatalf("day series: %v", err)
	}
	if days[edge.Format(dayLayout)]["SENT"] != 1 {
		t.Errorf("day counters = %v; want the cutoff's own day kept", days)
	}
}

// Resource samples older than the horizon go, newer ones stay — and the
// boundary is the one GetResourceHistory reads by.
func TestPruneResourceHistory(t *testing.T) {
	s := newRetentionStore(t)
	st := s.(*store)

	now := time.Now()
	for _, age := range []time.Duration{10 * 24 * time.Hour, 8 * 24 * time.Hour, time.Hour} {
		at := now.Add(-age)
		key := []byte(fmt.Sprintf("resource:%019d", math.MaxInt64-at.UnixNano()))
		if err := st.db.Set(key, []byte(fmt.Sprintf(`{"timestamp":%q}`, at.Format(time.RFC3339Nano))), nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	removed, err := s.PruneResourceHistoryBefore(t.Context(), now.AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("PruneResourceHistoryBefore: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed %d samples; want the two older than a week", removed)
	}
	left, err := s.GetResourceHistory(t.Context(), time.Time{})
	if err != nil {
		t.Fatalf("GetResourceHistory: %v", err)
	}
	if len(left) != 1 {
		t.Errorf("%d samples left; want the recent one", len(left))
	}
}

func waitForCount(t *testing.T, st *store, prefix string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(scanKeys(t, st.db, prefix)) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never saw %d keys under %s", want, prefix)
}
