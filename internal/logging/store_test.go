package logging

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPebbleStore(t *testing.T) {
	dir := "test_logs.db"
	defer os.RemoveAll(dir)

	store, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	entry := LogEntry{
		ID:        "test-1",
		TenantID:  "tenant-1",
		Timestamp: time.Now(),
		Level:     "INFO",
		Message:   "test message",
	}

	// Write
	if err := store.Write(entry); err != nil {
		t.Errorf("failed to write: %v", err)
	}

	// Wait for async write
	time.Sleep(1 * time.Second)

	// List
	entries, _, err := store.List(10, "", "tenant-1")
	if err != nil {
		t.Errorf("failed to list: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	} else if entries[0].ID != entry.ID {
		t.Errorf("expected ID %s, got %s", entry.ID, entries[0].ID)
	}

	// Close
	if err := store.Close(); err != nil {
		t.Errorf("failed to close: %v", err)
	}
}

func TestPebbleStore_ClosePanic(t *testing.T) {
	dir := "test_logs_panic.db"
	defer os.RemoveAll(dir)

	store, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Write many entries
	for i := range 100 {
		_ = store.Write(LogEntry{
			ID:        "test-panic",
			Timestamp: time.Now().Add(time.Duration(i) * time.Nanosecond),
		})
	}

	// Immediately close. This should not panic.
	if err := store.Close(); err != nil {
		t.Errorf("failed to close: %v", err)
	}
}

func TestPebbleStore_Subscribe(t *testing.T) {
	dir := "test_logs_sub.db"
	defer os.RemoveAll(dir)

	store, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch := store.Subscribe(ctx)

	entry := LogEntry{
		ID:        "test-sub",
		Timestamp: time.Now(),
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = store.Write(entry)
	}()

	select {
	case got := <-ch:
		if got.ID != entry.ID {
			t.Errorf("expected ID %s, got %s", entry.ID, got.ID)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for subscription")
	}
}
