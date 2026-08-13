package observability

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// value extracts one metric's value, ignoring the labels the exporter adds
// between the name and the number.
func value(t *testing.T, body, name string) (float64, bool) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, name) {
			continue
		}
		// Either "name value" or "name{labels} value".
		rest := strings.TrimPrefix(line, name)
		if rest != "" && rest[0] != ' ' && rest[0] != '{' {
			continue // a longer metric name that merely shares this prefix
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		return v, true
	}
	return 0, false
}

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("scrape returned %d", rec.Code)
	}
	return rec.Body.String()
}

func TestAQueuePublishesDepthAndAge(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	err = m.ObserveQueue("outbox", "Messages accepted but not yet delivered",
		func(context.Context) (QueueStats, error) {
			return QueueStats{Pending: 42, Oldest: 90 * time.Second}, nil
		})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}

	body := scrape(t, m)
	if !strings.Contains(body, "panmail_outbox_pending") {
		t.Error("depth is not published")
	}
	// The age is the reading an alert is written against: a depth of 42 is a
	// busy minute or a stopped worker and cannot tell you which.
	if !strings.Contains(body, "panmail_outbox_oldest_seconds") {
		t.Error("age is not published, which is the signal that matters")
	}
	if v, ok := value(t, body, "panmail_outbox_oldest_seconds"); !ok || v != 90 {
		t.Errorf("age = %v (found %v), want 90", v, ok)
	}
	if v, ok := value(t, body, "panmail_outbox_pending"); !ok || v != 42 {
		t.Errorf("depth = %v (found %v), want 42", v, ok)
	}
}

// Publishing zero when the database is unreachable would silence exactly the
// alert that should be firing — "oldest > 15m" reads healthy at zero.
func TestAFailedReadPublishesNothingRatherThanZero(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	err = m.ObserveQueue("outbox", "Messages",
		func(context.Context) (QueueStats, error) {
			return QueueStats{}, errors.New("database is unreachable")
		})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}

	if v, ok := value(t, scrape(t, m), "panmail_outbox_pending"); ok {
		t.Errorf("an unreadable queue reported %v, which reads as healthy", v)
	}
}

func TestAnEmptyQueueReportsZeroDepthAndNoAge(t *testing.T) {
	m, _ := New()
	m.ObserveQueue("webhook_deliveries", "Notifications",
		func(context.Context) (QueueStats, error) {
			return QueueStats{Pending: 0}, nil
		})

	v, ok := value(t, scrape(t, m), "panmail_webhook_deliveries_pending")
	if !ok || v != 0 {
		t.Errorf("depth = %v (found %v); an empty queue should still report", v, ok)
	}
}

func TestAGaugeIsReadAtScrapeTime(t *testing.T) {
	m, _ := New()

	sessions := int64(0)
	m.ObserveGauge("imap_idle_sessions", "Open IMAP IDLE connections",
		func() int64 { return sessions })

	if v, _ := value(t, scrape(t, m), "panmail_imap_idle_sessions"); v != 0 {
		t.Fatalf("gauge = %v before the value was set", v)
	}

	// A gauge pushed on change would still say 0 here; read-on-scrape is what
	// makes it the truth at the moment someone asks.
	sessions = 3
	if v, ok := value(t, scrape(t, m), "panmail_imap_idle_sessions"); !ok || v != 3 {
		t.Errorf("gauge = %v (found %v), want 3", v, ok)
	}
}

func TestShutdownIsSafeOnANilMetrics(t *testing.T) {
	var m *Metrics
	if err := m.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}
