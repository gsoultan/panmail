package postgres

import (
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
	"github.com/gsoultan/panmail/internal/event/repositories/stores/pebble"
	"github.com/gsoultan/panmail/internal/storetest"
)

// In the shadow phase each event reaches the local store once. The wrapper
// writes the primary itself, and a shadow that also mirrored to the same
// local store merged every counter twice: one SENT read as two on the
// dashboard for as long as the shadow ran.
func TestTheShadowDoesNotWriteThePrimaryAgain(t *testing.T) {
	local, err := pebble.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("open pebble: %v", err)
	}
	conn := storetest.NewConnection(t)
	w := NewWriter(conn)
	repo := stores.WithShadow(local, NewShadow(conn, local, w))
	t.Cleanup(func() { _ = repo.Close() })

	if err := repo.Write(t.Context(), ev("once", "a@example.com",
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, time.Now().UTC())); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var sent int64
	for range 100 {
		metrics, err := local.GetMetrics(t.Context(), storetest.TenantA, time.Time{}, time.Time{})
		if err != nil {
			t.Fatalf("GetMetrics: %v", err)
		}
		if sent = metrics["SENT"]; sent > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Give a second write, if there is one, its chance to land too.
	time.Sleep(250 * time.Millisecond)
	metrics, err := local.GetMetrics(t.Context(), storetest.TenantA, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if metrics["SENT"] != 1 {
		t.Errorf("the local store counted SENT %d times for one event", metrics["SENT"])
	}
}
