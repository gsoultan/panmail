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

func TestTimelineIdenticalTimestamp(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pebble-test-identical-*")
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
	messageID := "msg-identical"
	recipient := "user@example.com"

	// SENT and DELIVERED with the EXACT same timestamp
	now := time.Now().Truncate(time.Second)

	_ = s.Write(ctx, &entities.EmailEvent{
		ID:        "event-sent",
		TenantID:  tenantID,
		MessageID: messageID,
		Recipient: recipient,
		Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
		Timestamp: now,
	})

	_ = s.Write(ctx, &entities.EmailEvent{
		ID:        "event-delivered",
		TenantID:  tenantID,
		MessageID: messageID,
		Recipient: recipient,
		Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
		Timestamp: now,
	})

	// Wait for worker
	time.Sleep(200 * time.Millisecond)

	// Timeline check
	res, _, err := s.List(ctx, tenantID, stores.ListFilter{
		MessageID:      messageID,
		Recipient:      recipient,
		RecipientExact: true,
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(res) != 2 {
		t.Errorf("expected 2 events in timeline, got %d", len(res))
	}

	// Latest check: should be DELIVERED
	resLatest, _, err := s.List(ctx, tenantID, stores.ListFilter{
		LatestOnly:     true,
		Recipient:      recipient,
		RecipientExact: true,
	})
	if err != nil {
		t.Fatalf("List latest failed: %v", err)
	}
	if len(resLatest) != 1 {
		t.Errorf("expected 1 latest event, got %d", len(resLatest))
	} else if resLatest[0].Type != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED {
		t.Errorf("expected latest event to be DELIVERED, got %s", resLatest[0].Type)
	}
}
