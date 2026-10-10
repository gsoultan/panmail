package pebble

import (
	"context"
	"fmt"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// BenchmarkRangedGetMetrics measures the dashboard's headline totals over a
// closed range. They are tiled from day counters with only the ragged edges
// read from hour and minute ones, so the cost tracks the number of days, not
// the number of minutes or events in the range. A change that falls back to
// summing minutes, or to counting events, shows here as a large jump that
// grows with the range.
func BenchmarkRangedGetMetrics(b *testing.B) {
	s, err := NewStore(b.TempDir())
	if err != nil {
		b.Fatalf("NewStore: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	// An event every 37 minutes for ninety days, alternating type.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const events = 90 * 24 * 60 / 37
	var last string
	for i := range events {
		typ := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT
		if i%3 == 0 {
			typ = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED
		}
		last = fmt.Sprintf("e%06d", i)
		if err := s.Write(ctx, &entities.EmailEvent{
			ID: last, TenantID: retentionTenant, Type: typ,
			Timestamp: base.Add(time.Duration(i*37) * time.Minute),
		}); err != nil {
			b.Fatalf("Write: %v", err)
		}
	}
	// The writer is asynchronous; wait for the last event to land.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if got, _ := s.GetByID(ctx, retentionTenant, last); got != nil {
			break
		}
		if time.Now().After(deadline) {
			b.Fatal("the events never became readable")
		}
		time.Sleep(10 * time.Millisecond)
	}

	at := func(day, hour, minute int) time.Time {
		return base.AddDate(0, 0, day).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
	}
	for _, r := range []struct {
		name       string
		start, end time.Time
	}{
		{"one-day-ragged", at(10, 7, 13), at(10, 18, 41)},
		{"thirty-days-ragged", at(20, 7, 13), at(50, 18, 41)},
		{"eighty-days-aligned", at(5, 0, 0), at(84, 23, 59)},
	} {
		b.Run(r.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				got, err := s.GetMetrics(ctx, retentionTenant, r.start, r.end)
				if err != nil {
					b.Fatalf("GetMetrics: %v", err)
				}
				if got["SENT"] == 0 {
					b.Fatal("nothing counted in a range the fixture fills")
				}
			}
		})
	}
}
