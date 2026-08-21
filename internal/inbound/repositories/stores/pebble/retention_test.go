package pebble

import (
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
)

const testTenant = "tenant-a"

func newTestStore(t *testing.T) stores.InboundRepository {
	t.Helper()

	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// write stores a message and waits for the async writer to flush it, so a
// prune that follows sees it.
func write(t *testing.T, s stores.InboundRepository, e *entities.InboundEmail) {
	t.Helper()

	if err := s.Write(t.Context(), e); err != nil {
		t.Fatalf("failed to write %s: %v", e.ID, err)
	}
	for range 100 {
		if got, _ := s.GetByID(t.Context(), testTenant, e.ID); got != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("inbound message %s never became readable", e.ID)
}

func inbound(id string, age time.Duration) *entities.InboundEmail {
	return &entities.InboundEmail{
		ID:        id,
		TenantID:  testTenant,
		From:      "sender@example.com",
		To:        []string{"inbox@example.com"},
		Subject:   "subject " + id,
		Timestamp: time.Now().Add(-age),
	}
}

func TestTruncateBefore(t *testing.T) {
	tests := []struct {
		name        string
		age         time.Duration
		cutoff      time.Duration
		wantRemoved int64
	}{
		{name: "older than the cutoff goes", age: 48 * time.Hour, cutoff: 24 * time.Hour, wantRemoved: 1},
		{name: "newer than the cutoff stays", age: time.Hour, cutoff: 24 * time.Hour, wantRemoved: 0},
		{
			// Inbound retention defaults to off, and this is why: the cutoff
			// is the only thing standing between a policy and somebody's mail.
			name:        "a cutoff at the epoch removes nothing",
			age:         48 * time.Hour,
			cutoff:      365 * 24 * time.Hour,
			wantRemoved: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			write(t, s, inbound("i1", tc.age))

			removed, err := s.TruncateBefore(t.Context(), time.Now().Add(-tc.cutoff))
			if err != nil {
				t.Fatalf("TruncateBefore: %v", err)
			}
			if removed != tc.wantRemoved {
				t.Errorf("removed = %d; want %d", removed, tc.wantRemoved)
			}

			got, _ := s.GetByID(t.Context(), testTenant, "i1")
			if (got == nil) != (tc.wantRemoved == 1) {
				t.Errorf("message present = %v; want %v", got != nil, tc.wantRemoved == 0)
			}
		})
	}
}

func TestTruncateBeforeKeepsNewerMail(t *testing.T) {
	s := newTestStore(t)
	write(t, s, inbound("old", 48*time.Hour))
	write(t, s, inbound("fresh", time.Hour))

	removed, err := s.TruncateBefore(t.Context(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d; want 1", removed)
	}

	if got, _ := s.GetByID(t.Context(), testTenant, "fresh"); got == nil {
		t.Error("pruning old mail took recent mail with it")
	}

	// The listing reads the index, not the row, so a prune that misses the
	// index leaves deleted mail visible until someone opens it.
	list, _, err := s.List(t.Context(), testTenant, 10, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != "fresh" {
		t.Errorf("list = %v; want only the fresh message", list)
	}
}

// Inbound holds the only copy panmail has of mail somebody was sent, so an
// unset cutoff must never be read as "expire everything".
func TestTruncateBeforeRefusesAZeroCutoff(t *testing.T) {
	s := newTestStore(t)
	write(t, s, inbound("i1", 48*time.Hour))

	removed, err := s.TruncateBefore(t.Context(), time.Time{})
	if err == nil {
		t.Fatalf("a zero cutoff was accepted and removed %d messages", removed)
	}
	if got, _ := s.GetByID(t.Context(), testTenant, "i1"); got == nil {
		t.Error("mail was deleted despite the prune refusing")
	}
}
