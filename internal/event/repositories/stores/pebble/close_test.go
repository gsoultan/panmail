package pebble

import (
	"errors"
	"fmt"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// Every write Close finds still buffered has to reach the disk. A caller was
// told each one had been accepted, and the buffer is 20,000 deep: before the
// drain, the stop case could win the select with almost all of it queued, and
// a probe of 20,000 writes followed by Close kept about a thousand.
func TestCloseCommitsEverythingAlreadyAccepted(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	const writes = 20000
	at := time.Now()
	for i := range writes {
		if err := s.Write(t.Context(), &entities.EmailEvent{
			ID:        fmt.Sprintf("e%05d", i),
			TenantID:  "t1",
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
			Timestamp: at,
		}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	metrics, err := reopened.GetMetrics(t.Context(), "t1", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if got := metrics["SENT"]; got != writes {
		t.Fatalf("after Close the store holds %d of %d accepted writes", got, writes)
	}
	for _, id := range []string{"e00000", fmt.Sprintf("e%05d", writes-1)} {
		if e, _ := reopened.GetByID(t.Context(), "t1", id); e == nil {
			t.Errorf("event %s was accepted and then lost", id)
		}
	}
}

// After Close a write is refused, not dropped and not left blocking on a
// channel nobody reads any more.
func TestWritesAfterCloseAreRefused(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	cases := map[string]func() error{
		"event": func() error {
			return s.Write(t.Context(), &entities.EmailEvent{ID: "late", TenantID: "t1", Timestamp: time.Now()})
		},
		"message": func() error {
			return s.WriteMessage(t.Context(), &entities.EmailMessage{ID: "late", TenantID: "t1", CreatedAt: time.Now()})
		},
		"resource": func() error {
			return s.WriteResourceMetric(t.Context(), 1, 1, 1)
		},
	}
	for name, write := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { done <- write() }()
			select {
			case err := <-done:
				if !errors.Is(err, ErrClosed) {
					t.Errorf("write after Close returned %v; want ErrClosed", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("write after Close blocked")
			}
		})
	}

	// A second Close is harmless: the shared store and its wrappers can both
	// reach this one on the way down.
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
