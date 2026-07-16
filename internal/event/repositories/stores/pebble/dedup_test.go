package pebble

import (
	"fmt"
	"os"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

func TestListLatestOnly(t *testing.T) {
	dir := "test_dedup.db"
	_ = os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := t.Context()
	tenantID := "test-tenant"
	messageID := "msg-1"

	// 1. Write multiple events for the same message ID
	events := []*entities.EmailEvent{
		{
			ID:        "e1",
			TenantID:  tenantID,
			MessageID: messageID,
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
			Timestamp: time.Now().Add(-10 * time.Minute),
		},
		{
			ID:        "e2",
			TenantID:  tenantID,
			MessageID: messageID,
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
			Timestamp: time.Now().Add(-5 * time.Minute),
		},
		{
			ID:        "e3", // This is the latest
			TenantID:  tenantID,
			MessageID: messageID,
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED,
			Timestamp: time.Now(),
		},
	}

	for _, e := range events {
		if err := store.Write(ctx, e); err != nil {
			t.Fatalf("failed to write event: %v", err)
		}
	}

	// Wait for async processing
	time.Sleep(1 * time.Second)

	// 2. List all events (should return 3)
	allEvents, _, err := store.List(ctx, tenantID, stores.ListFilter{
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("failed to list all events: %v", err)
	}
	if len(allEvents) != 3 {
		t.Errorf("expected 3 events, got %d", len(allEvents))
	}

	// 3. List with LatestOnly=true (should return 1)
	latestEvents, _, err := store.List(ctx, tenantID, stores.ListFilter{
		PageSize:   10,
		LatestOnly: true,
	})
	if err != nil {
		t.Fatalf("failed to list latest events: %v", err)
	}
	if len(latestEvents) != 1 {
		t.Errorf("expected 1 event, got %d", len(latestEvents))
	} else {
		if latestEvents[0].ID != "e3" {
			t.Errorf("expected latest event e3, got %s", latestEvents[0].ID)
		}
		if latestEvents[0].Type != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED {
			t.Errorf("expected status OPENED, got %v", latestEvents[0].Type)
		}
	}
}

func TestListLatestOnlyPagination(t *testing.T) {
	dir := "test_dedup_paging.db"
	_ = os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := t.Context()
	tenantID := "test-tenant"

	// Write 5 messages with 2 events each
	for i := 1; i <= 5; i++ {
		msgID := fmt.Sprintf("msg-%d", i)
		// Old event
		e1 := &entities.EmailEvent{
			ID:        fmt.Sprintf("e%d-1", i),
			TenantID:  tenantID,
			MessageID: msgID,
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
			Timestamp: time.Now().Add(-1 * time.Hour),
		}
		_ = store.Write(ctx, e1)

		// Newer event
		e2 := &entities.EmailEvent{
			ID:        fmt.Sprintf("e%d-2", i),
			TenantID:  tenantID,
			MessageID: msgID,
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
			Timestamp: time.Now().Add(time.Duration(i) * time.Minute), // Each message has a different latest time
		}
		_ = store.Write(ctx, e2)
	}

	time.Sleep(1 * time.Second)

	// List with PageSize=2
	res1, token1, err := store.List(ctx, tenantID, stores.ListFilter{
		PageSize:   2,
		LatestOnly: true,
	})
	for i, e := range res1 {
		t.Logf("Page 1 Item %d: %s %s", i, e.ID, e.MessageID)
	}
	if len(res1) != 2 {
		t.Errorf("page 1: expected 2, got %d", len(res1))
	}
	if token1 == "" {
		t.Error("page 1: expected token")
	}

	res2, token2, err := store.List(ctx, tenantID, stores.ListFilter{
		PageSize:   2,
		PageToken:  token1,
		LatestOnly: true,
	})
	for i, e := range res2 {
		t.Logf("Page 2 Item %d: %s %s", i, e.ID, e.MessageID)
	}
	if len(res2) != 2 {
		t.Errorf("page 2: expected 2, got %d", len(res2))
	}
	if token2 == "" {
		t.Error("page 2: expected token")
	}

	res3, _, err := store.List(ctx, tenantID, stores.ListFilter{
		PageSize:   2,
		PageToken:  token2,
		LatestOnly: true,
	})
	for i, e := range res3 {
		t.Logf("Page 3 Item %d: %s %s", i, e.ID, e.MessageID)
	}
	if len(res3) != 1 {
		t.Errorf("page 3: expected 1, got %d", len(res3))
	}
}
