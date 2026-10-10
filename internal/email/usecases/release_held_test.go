package usecases

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	outboxstore "github.com/gsoultan/panmail/internal/email/repositories/stores/postgres"
	"github.com/gsoultan/panmail/internal/storetest"
)

// Against the real store, because the bug was the seam: the releaser checked
// for a nil row and the store never returned one, answering a missing row with
// sql.ErrNoRows instead. A discard retried after the row had gone -- a reject
// pressed twice, an expiry sweep reaching a row a reject already removed --
// failed rather than reporting work already done.
func TestDiscardingAHeldMessageTwiceSucceeds(t *testing.T) {
	ctx := context.Background()
	outbox := outboxstore.NewOutboxStore(storetest.NewConnection(t))
	id := storetest.ID("held-1")
	now := time.Now()
	if err := outbox.Create(ctx, &entities.OutboxEmail{
		ID: id, TenantID: storetest.TenantA, Request: []byte(`{}`),
		Status: entities.OutboxStatusHeld, NextRetryAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seeding the held row: %v", err)
	}

	releaser := NewHeldReleaser(outbox, nil)
	if err := releaser.DiscardHeld(ctx, storetest.TenantA, id); err != nil {
		t.Fatalf("first discard: %v", err)
	}
	if err := releaser.DiscardHeld(ctx, storetest.TenantA, id); err != nil {
		t.Fatalf("second discard of a message already gone = %v, want success", err)
	}

	// Releasing what is gone is still an error: a reviewer told a message went
	// out has to be right.
	if err := releaser.ReleaseHeld(ctx, storetest.TenantA, id); err == nil {
		t.Fatal("releasing a message that is no longer in the outbox reported success")
	}
}
