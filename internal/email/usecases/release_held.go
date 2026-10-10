package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/email/repositories/stores"
	"github.com/gsoultan/panmail/internal/emailfilter"
)

// heldReleaser returns quarantined outbound mail to the outbox worker, or
// throws it away. It satisfies emailfilter.OutboundReleaser.
type heldReleaser struct {
	outbox stores.OutboxRepository
	worker QueueWorker
}

// NewHeldReleaser wires the review queue back to the outbox.
func NewHeldReleaser(outbox stores.OutboxRepository, worker QueueWorker) emailfilter.OutboundReleaser {
	return &heldReleaser{outbox: outbox, worker: worker}
}

// ReleaseHeld moves a held row back to PENDING and wakes the worker.
//
// Idempotent on purpose. A release retried after a partial failure must be
// safe, and a row that is already PENDING is left alone rather than reset —
// resetting would clear a retry schedule the worker had already started.
func (r *heldReleaser) ReleaseHeld(ctx context.Context, tenantID, messageID string) error {
	email, err := r.outbox.GetByID(ctx, messageID)
	if err != nil {
		return fmt.Errorf("reading the held message: %w", err)
	}
	if email == nil {
		return fmt.Errorf("held message %s is no longer in the outbox", messageID)
	}
	// Scoped even though the id is a UUID. An id that leaked from one tenant
	// must not release another tenant's mail, and the check costs nothing.
	if email.TenantID != tenantID {
		return fmt.Errorf("held message %s does not belong to this tenant", messageID)
	}
	if email.Status != entities.OutboxStatusHeld {
		return nil
	}

	email.Status = entities.OutboxStatusPending
	email.NextRetryAt = time.Now()
	email.UpdatedAt = time.Now()
	if err := r.outbox.Update(ctx, email); err != nil {
		return fmt.Errorf("releasing the held message: %w", err)
	}

	if r.worker != nil {
		r.worker.Trigger()
	}
	return nil
}

// DiscardHeld removes the payload of a message a reviewer refused, or one that
// expired unreviewed. Idempotent: discarding a message that is no longer in the
// outbox succeeds.
func (r *heldReleaser) DiscardHeld(ctx context.Context, tenantID, messageID string) error {
	email, err := r.outbox.GetByID(ctx, messageID)
	if err != nil {
		return fmt.Errorf("reading the held message: %w", err)
	}
	// Already gone is already discarded. A reject retried after a timeout, or
	// an expiry sweep reaching a row a reviewer's reject already removed, has
	// nothing left to do and must not report a failure for it.
	if email == nil {
		return nil
	}
	if email.TenantID != tenantID {
		return fmt.Errorf("held message %s does not belong to this tenant", messageID)
	}
	// Only a held row is ours to delete. Anything else has been claimed or
	// sent, and removing it would be racing the worker.
	if email.Status != entities.OutboxStatusHeld {
		return nil
	}
	return r.outbox.Delete(ctx, messageID)
}

// PruneHeld removes held messages older than the cutoff that no review can
// still release. It satisfies emailfilter.HeldPruner, which is how the
// quarantine's retention reaches the outbox without importing it.
func (r *heldReleaser) PruneHeld(ctx context.Context, olderThan time.Time) (int64, error) {
	return r.outbox.PruneHeld(ctx, olderThan)
}

var _ emailfilter.HeldPruner = (*heldReleaser)(nil)
