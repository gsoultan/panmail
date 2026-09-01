package usecases

import (
	"context"
	"fmt"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// heldReleaser announces an inbound message a rule held back.
//
// A hold does not discard inbound mail — the message is written to the store
// on arrival, and what the hold suppresses is the webhook. So releasing is
// firing the notification that was withheld, which is why this reads the
// stored message rather than needing a payload kept anywhere else.
type heldReleaser struct {
	repo    stores.InboundRepository
	trigger WebhookTrigger
}

// NewHeldReleaser wires the review queue back to the inbound webhook.
func NewHeldReleaser(repo stores.InboundRepository, trigger WebhookTrigger) emailfilter.InboundReleaser {
	return &heldReleaser{repo: repo, trigger: trigger}
}

// ReleaseHeld fires the webhook the hold withheld.
//
// Reported rather than swallowed when the message cannot be found: the review
// queue has already recorded the release by the time this runs, and a reviewer
// told the message went out has to be right about that.
func (r *heldReleaser) ReleaseHeld(ctx context.Context, tenantID, messageID string) error {
	if r.trigger == nil {
		return fmt.Errorf("no inbound webhook trigger is configured")
	}

	// Scoped to the tenant even though the id is unique. An id that leaked
	// from one tenant must not announce another tenant's mail.
	stored, err := r.repo.GetByID(ctx, tenantID, messageID)
	if err != nil {
		return fmt.Errorf("reading the held message: %w", err)
	}
	if stored == nil {
		return fmt.Errorf("held message %s is no longer stored", messageID)
	}

	// Rebuilt from what was stored rather than from what arrived, because what
	// arrived is long gone by the time anyone reviews it. The shape is the one
	// Process sends, so a subscriber cannot tell a released message from one
	// that was never held — which is the point.
	r.trigger.Enqueue(tenantID, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_INBOUND,
		&panmailv1.InboundEmail{
			Id:        stored.ID,
			TenantId:  stored.TenantID,
			From:      stored.From,
			To:        stored.To,
			Subject:   stored.Subject,
			BodyHtml:  stored.BodyHTML,
			BodyText:  stored.BodyText,
			Headers:   stored.Headers,
			Timestamp: timestamppb.New(stored.Timestamp),
		})
	return nil
}
