package pebble

import (
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
