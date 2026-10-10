package pebble

import (
	"fmt"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

// writeAndSettle writes events and waits until the writer has committed them.
func writeAndSettle(t *testing.T, s stores.EventRepository, events []*entities.EmailEvent) {
	t.Helper()
	for _, e := range events {
		if err := s.Write(t.Context(), e); err != nil {
			t.Fatalf("write %s: %v", e.ID, err)
		}
	}
	waitLong(t, s, events[len(events)-1].ID)
}

// Counters are bucketed in UTC whatever zone the event was stamped in.
//
// RecordEvent stamps events with time.Now(), which is the server's local zone,
// and the writer named the bucket from that, while every reader asks in UTC.
// An event at 03:00 on the 1st in Jakarta is 20:00 on the 31st in UTC; the
// writer filed it under the 1st, and a UTC chart of the 31st did not have it.
func TestCountersAreBucketedInUTC(t *testing.T) {
	jakarta := time.FixedZone("WIB", 7*60*60)
	at := time.Date(2026, 1, 1, 3, 30, 0, 0, jakarta) // 2025-12-31 20:30 UTC

	tests := []struct {
		granularity string
		bucket      string
	}{
		{"day", "2025-12-31"},
		{"hour", "2025-12-31 20"},
		{"minute", "2025-12-31 20:30"},
	}

	s := newRetentionStore(t)
	writeAndSettle(t, s, []*entities.EmailEvent{{
		ID: "e1", TenantID: retentionTenant, Timestamp: at,
		Type: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
	}})

	for _, tt := range tests {
		t.Run(tt.granularity, func(t *testing.T) {
			start := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
			end := time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC)
			got, err := s.GetTimeSeriesMetrics(t.Context(), retentionTenant, start, end, tt.granularity)
			if err != nil {
				t.Fatalf("GetTimeSeriesMetrics: %v", err)
			}
			if got[tt.bucket]["SENT"] != 1 {
				t.Errorf("buckets = %v; want SENT=1 under %q", got, tt.bucket)
			}
		})
	}

	// The same instant asked for in another zone names the same buckets.
	t.Run("bounds in another zone", func(t *testing.T) {
		start := time.Date(2026, 1, 1, 3, 0, 0, 0, jakarta)
		end := time.Date(2026, 1, 1, 4, 0, 0, 0, jakarta)
		got, err := s.GetTimeSeriesMetrics(t.Context(), retentionTenant, start, end, "minute")
		if err != nil {
			t.Fatalf("GetTimeSeriesMetrics: %v", err)
		}
		if got["2025-12-31 20:30"]["SENT"] != 1 {
			t.Errorf("buckets = %v; want the event under its UTC minute", got)
		}
	})
}

// minuteSum is how GetMetrics answered a range before it used day and hour
// buckets: every minute counter in the range, added up. Kept here as the
// reference the new answer has to equal.
func minuteSum(t *testing.T, s stores.EventRepository, start, end time.Time) map[string]int64 {
	t.Helper()
	ts, err := s.GetTimeSeriesMetrics(t.Context(), retentionTenant, start, end, "minute")
	if err != nil {
		t.Fatalf("minute series: %v", err)
	}
	sum := make(map[string]int64)
	for _, bucket := range ts {
		for k, v := range bucket {
			sum[k] += v
		}
	}
	return sum
}

// A ranged GetMetrics reads whole days from day counters and only the ragged
// edges from hour and minute ones. The answer has to be exactly what summing
// every minute gave, on every shape of range: aligned, ragged at either end,
// inside one hour, inside one day, and across months.
func TestRangedMetricsMatchTheMinuteSum(t *testing.T) {
	s := newRetentionStore(t)

	// Events every 37 minutes for twelve days, so every kind of edge has
	// something in it, alternating type so the per-type split is checked too.
	base := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)
	var events []*entities.EmailEvent
	for i := range 12 * 24 * 60 / 37 {
		typ := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT
		if i%3 == 0 {
			typ = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED
		}
		events = append(events, &entities.EmailEvent{
			ID:        fmt.Sprintf("e%05d", i),
			TenantID:  retentionTenant,
			Type:      typ,
			Timestamp: base.Add(time.Duration(i*37) * time.Minute),
		})
	}
	writeAndSettle(t, s, events)

	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, 2, 25+day, hour, minute, 0, 0, time.UTC)
	}
	tests := []struct {
		name       string
		start, end time.Time
	}{
		{"whole days", at(1, 0, 0), at(5, 23, 59)},
		{"ragged both ends", at(0, 7, 13), at(9, 18, 41)},
		{"ragged start, aligned end", at(2, 5, 1), at(6, 23, 59)},
		{"aligned start, ragged end", at(2, 0, 0), at(6, 3, 59)},
		{"within one hour", at(3, 4, 10), at(3, 4, 50)},
		{"within one day", at(3, 4, 10), at(3, 19, 2)},
		{"across a month", at(2, 22, 22), at(5, 1, 1)},
		{"one minute", at(2, 13, 40), at(2, 13, 40)},
		{"end has seconds", at(1, 1, 1), at(2, 2, 2).Add(59 * time.Second)},
		{"bounds in another zone", at(1, 1, 1).In(time.FixedZone("x", -5*3600)), at(7, 7, 7).In(time.FixedZone("y", 9*3600))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := minuteSum(t, s, tt.start, tt.end)
			got, err := s.GetMetrics(t.Context(), retentionTenant, tt.start, tt.end)
			if err != nil {
				t.Fatalf("GetMetrics: %v", err)
			}
			if want["SENT"] == 0 {
				t.Fatal("the fixture has nothing in this range, so equality proves nothing")
			}
			for _, k := range []string{"SENT", "DELIVERED"} {
				if got[k] != want[k] {
					t.Errorf("%s = %d; the minute sum says %d", k, got[k], want[k])
				}
			}
		})
	}

	// An inverted range is empty. Not compared with the minute sum, because
	// that read handed Pebble a lower bound above its upper one, which its
	// invariant checks treat as a crash.
	t.Run("end before start", func(t *testing.T) {
		got, err := s.GetMetrics(t.Context(), retentionTenant, at(4, 12, 5), at(3, 12, 5))
		if err != nil {
			t.Fatalf("GetMetrics: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v for a range that ends before it starts", got)
		}
	})

	// And the whole days really come from day counters: with every minute
	// counter gone, a range of whole days still has its answer. The events go
	// too, or the fallback that counts them would answer instead.
	t.Run("whole days do not read minutes", func(t *testing.T) {
		start, end := at(1, 0, 0), at(5, 23, 59)
		want := minuteSum(t, s, start, end)

		db := s.(*store).db
		for _, span := range [][2]string{
			{"timeseries:minute:", "timeseries:minute;"},
			{"events:", "events;"},
		} {
			if err := db.DeleteRange([]byte(span[0]), []byte(span[1]), nil); err != nil {
				t.Fatalf("delete %s: %v", span[0], err)
			}
		}
		got, err := s.GetMetrics(t.Context(), retentionTenant, start, end)
		if err != nil {
			t.Fatalf("GetMetrics: %v", err)
		}
		if got["SENT"] != want["SENT"] || got["DELIVERED"] != want["DELIVERED"] {
			t.Errorf("got %v without minute counters; want %v", got, want)
		}
	})
}
