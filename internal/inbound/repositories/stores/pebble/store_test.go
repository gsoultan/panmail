package pebble

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
)

func message(id string, at time.Time) *entities.InboundEmail {
	return &entities.InboundEmail{
		ID: id, TenantID: testTenant, From: "a@example.com",
		To: []string{"b@example.com"}, Subject: "hello", BodyText: "body", Timestamp: at,
	}
}

// The de-duplication between the poller and IDLE is a GetByID before the
// write. When Write only queued, the lookup straight after it missed, both
// paths saw the message as new, and its bounce was recorded and the tenant's
// webhook fired twice.
func TestAWrittenMessageIsReadableImmediately(t *testing.T) {
	s := newTestStore(t)

	for i := range 50 {
		id := fmt.Sprintf("msg-%d", i)
		if err := s.Write(t.Context(), message(id, time.Now())); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := s.GetByID(t.Context(), testTenant, id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got == nil {
			t.Fatalf("%s was not readable straight after Write returned", id)
		}
	}
}

// Close used to stop the background writer without draining its queue. With
// three thousand writes outstanding, nearly all of them were lost -- and every
// one had been reported to its caller as stored.
func TestEveryAcknowledgedWriteSurvivesClose(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	const writers, perWriter = 10, 300
	base := time.Now()
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range perWriter {
				id := fmt.Sprintf("w%d-%d", w, i)
				if err := s.Write(t.Context(), message(id, base.Add(time.Duration(w*perWriter+i)))); err != nil {
					t.Errorf("write %s: %v", id, err)
				}
			}
		})
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	count, err := reopened.Count(t.Context(), testTenant, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != writers*perWriter {
		t.Errorf("%d of %d acknowledged messages survived Close", count, writers*perWriter)
	}
}

// A write that cannot be stored has to say so, or the caller acknowledges mail
// it does not have.
func TestAWriteAfterCloseReportsAnError(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := s.Write(t.Context(), message("late", time.Now())); !errors.Is(err, ErrClosed) {
		t.Errorf("write after close returned %v, want ErrClosed", err)
	}
	// Closing twice is the shutdown path's business, not a crash.
	if err := s.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}
