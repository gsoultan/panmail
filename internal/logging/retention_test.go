package logging

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func newLogStore(t *testing.T) Store {
	t.Helper()

	s, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create log store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// writeLog stores an entry and waits for the async writer to flush it, so a
// prune that follows sees it.
func writeLog(t *testing.T, s Store, age time.Duration) LogEntry {
	t.Helper()

	entry := LogEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now().Add(-age),
		Level:     "INFO",
		Message:   "something happened",
		Service:   "test",
	}
	if err := s.Write(entry); err != nil {
		t.Fatalf("failed to write log entry: %v", err)
	}

	for range 100 {
		if contains(t, s, entry.ID) {
			return entry
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log entry %s never became readable", entry.ID)
	return entry
}

func contains(t *testing.T, s Store, id string) bool {
	t.Helper()

	entries, _, err := s.List(100, "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newLogStore(t)
			entry := writeLog(t, s, tc.age)

			removed, err := s.TruncateBefore(t.Context(), time.Now().Add(-tc.cutoff))
			if err != nil {
				t.Fatalf("TruncateBefore: %v", err)
			}
			if removed != tc.wantRemoved {
				t.Errorf("removed = %d; want %d", removed, tc.wantRemoved)
			}
			if got := contains(t, s, entry.ID); got == (tc.wantRemoved == 1) {
				t.Errorf("entry present = %v; want %v", got, tc.wantRemoved == 0)
			}
		})
	}
}

func TestTruncateBeforeKeepsRecentEntries(t *testing.T) {
	s := newLogStore(t)
	old := writeLog(t, s, 48*time.Hour)
	recent := writeLog(t, s, time.Minute)

	removed, err := s.TruncateBefore(t.Context(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d; want 1", removed)
	}

	if contains(t, s, old.ID) {
		t.Error("the expired entry survived")
	}
	if !contains(t, s, recent.ID) {
		t.Error("a recent entry was pruned with the old one")
	}
}

func TestTruncateBeforeRefusesAZeroCutoff(t *testing.T) {
	s := newLogStore(t)
	entry := writeLog(t, s, 48*time.Hour)

	removed, err := s.TruncateBefore(t.Context(), time.Time{})
	if err == nil {
		t.Fatalf("a zero cutoff was accepted and removed %d entries", removed)
	}
	if !contains(t, s, entry.ID) {
		t.Error("the entry was deleted despite the prune refusing")
	}
}
