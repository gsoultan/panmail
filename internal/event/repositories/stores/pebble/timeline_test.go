package pebble

import (
	"context"
	"os"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

func TestTimelineAccuracy(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pebble-test-timeline-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	tenantID := "tenant-1"
	messageID := "msg-1"

	r1 := "recipient1@example.com"
	r2 := "recipient2@example.com"

	// SENT for both
	t1 := time.Now().Add(-10 * time.Minute)
	_ = s.Write(ctx, &entities.EmailEvent{
		ID:        "e1",
		TenantID:  tenantID,
		MessageID: messageID,
		Recipient: r1,
		Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
		Timestamp: t1,
	})
	_ = s.Write(ctx, &entities.EmailEvent{
		ID:        "e2",
		TenantID:  tenantID,
		MessageID: messageID,
		Recipient: r2,
		Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
		Timestamp: t1,
	})

	// DELIVERED for r1 only
	t2 := time.Now().Add(-5 * time.Minute)
	_ = s.Write(ctx, &entities.EmailEvent{
		ID:        "e3",
		TenantID:  tenantID,
		MessageID: messageID,
		Recipient: r1,
		Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
		Timestamp: t2,
	})

	// Wait for worker to process
	time.Sleep(200 * time.Millisecond)

	// Test timeline for r1: should have 2 events
	res1, _, err := s.List(ctx, tenantID, stores.ListFilter{
		MessageID:      messageID,
		Recipient:      r1,
		RecipientExact: true,
		PageSize:       10,
	})
	if err != nil {
		t.Fatalf("List r1 failed: %v", err)
	}
	if len(res1) != 2 {
		t.Errorf("expected 2 events for r1, got %d", len(res1))
	}

	// Test timeline for r2: should have 1 event
	res2, _, err := s.List(ctx, tenantID, stores.ListFilter{
		MessageID:      messageID,
		Recipient:      r2,
		RecipientExact: true,
		PageSize:       10,
	})
	if err != nil {
		t.Fatalf("List r2 failed: %v", err)
	}
	if len(res2) != 1 {
		t.Errorf("expected 1 event for r2, got %d", len(res2))
	}

	// Test latest only view: should have 2 events (latest for r1 and latest for r2)
	resLatest, _, err := s.List(ctx, tenantID, stores.ListFilter{
		LatestOnly: true,
		PageSize:   10,
	})
	if err != nil {
		t.Fatalf("List latest failed: %v", err)
	}
	if len(resLatest) != 2 {
		t.Errorf("expected 2 latest events, got %d", len(resLatest))
	}
}
