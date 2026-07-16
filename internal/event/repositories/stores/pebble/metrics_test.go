package pebble

import (
	"os"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

func TestMetricsRealtimeAccurate(t *testing.T) {
	dir := "test_events.db"
	_ = os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := t.Context()
	tenantID := "test-tenant"

	// 1. Write events
	events := []*entities.EmailEvent{
		{
			ID:        "e1",
			TenantID:  tenantID,
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
			Timestamp: time.Now(),
		},
		{
			ID:        "e2",
			TenantID:  tenantID,
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
			Timestamp: time.Now(),
		},
		{
			ID:        "e3",
			TenantID:  tenantID,
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
			Timestamp: time.Now(),
		},
	}

	for _, e := range events {
		if err := store.Write(ctx, e); err != nil {
			t.Fatalf("failed to write event: %v", err)
		}
	}

	// Wait for async worker to process (ticker is 100ms)
	var metrics map[string]int64
	for range 10 {
		metrics, err = store.GetMetrics(ctx, tenantID, time.Time{}, time.Time{})
		if err == nil && metrics["SENT"] == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 2. Check metrics
	if err != nil {
		t.Fatalf("failed to get metrics: %v", err)
	}

	// Should have trimmed keys: "SENT" and "DELIVERED"
	if metrics["SENT"] != 2 {
		t.Errorf("expected SENT=2, got %v", metrics["SENT"])
	}
	if metrics["DELIVERED"] != 1 {
		t.Errorf("expected DELIVERED=1, got %v", metrics["DELIVERED"])
	}

	// Check if old full keys exist (they shouldn't)
	if _, ok := metrics["EMAIL_EVENT_TYPE_SENT"]; ok {
		t.Errorf("found unexpected key EMAIL_EVENT_TYPE_SENT")
	}

	// 3. Check time series
	tsMetrics, err := store.GetTimeSeriesMetrics(ctx, tenantID, time.Time{}, time.Time{}, "day")
	if err != nil {
		t.Fatalf("failed to get timeseries metrics: %v", err)
	}

	dateStr := time.Now().Format("2006-01-02")
	dayMetrics := tsMetrics[dateStr]
	if dayMetrics == nil {
		t.Fatalf("no metrics for today %s", dateStr)
	}

	if dayMetrics["SENT"] != 2 {
		t.Errorf("ts: expected SENT=2, got %v", dayMetrics["SENT"])
	}

	// 4. Check minute time series
	var minTsMetrics map[string]map[string]int64
	for range 10 {
		minTsMetrics, err = store.GetTimeSeriesMetrics(ctx, tenantID, time.Now().Add(-1*time.Minute), time.Now().Add(1*time.Minute), "minute")
		if err == nil {
			minuteStr := time.Now().Format("2006-01-02 15:04")
			if minTsMetrics[minuteStr] != nil && minTsMetrics[minuteStr]["SENT"] == 2 {
				break
			}
			// Also check previous minute in case boundary crossed
			minuteStr = time.Now().Add(-1 * time.Minute).Format("2006-01-02 15:04")
			if minTsMetrics[minuteStr] != nil && minTsMetrics[minuteStr]["SENT"] == 2 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("failed to get minute timeseries metrics: %v", err)
	}

	minuteStr := time.Now().Format("2006-01-02 15:04")
	minuteMetrics := minTsMetrics[minuteStr]
	if minuteMetrics == nil {
		minuteStr = time.Now().Add(-1 * time.Minute).Format("2006-01-02 15:04")
		minuteMetrics = minTsMetrics[minuteStr]
	}

	if minuteMetrics == nil {
		t.Fatalf("no metrics found for minute around %s", time.Now().Format("15:04"))
	}

	if minuteMetrics["SENT"] != 2 {
		t.Errorf("min ts: expected SENT=2, got %v", minuteMetrics["SENT"])
	}
}
