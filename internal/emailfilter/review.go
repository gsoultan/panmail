package emailfilter

import (
	"context"
	"fmt"
	"log/slog"
)

// OutboundReleaser puts a held outbound message back on the wire. Implemented
// in internal/email against the outbox, and declared here as a narrow
// interface so this package keeps depending on nothing.
type OutboundReleaser interface {
	// ReleaseHeld moves the held outbox row back to PENDING and wakes the
	// worker. It must be idempotent: a release retried after a partial failure
	// has to be safe, and moving an already-PENDING row to PENDING is.
	ReleaseHeld(ctx context.Context, tenantID, messageID string) error

	// DiscardHeld removes a held outbox row, for a message a reviewer refused.
	DiscardHeld(ctx context.Context, tenantID, messageID string) error
}

// InboundReleaser announces a held inbound message, which is the webhook that
// the hold suppressed.
type InboundReleaser interface {
	ReleaseHeld(ctx context.Context, tenantID, messageID string) error
}

// Reviewer is the review queue's two verbs.
type Reviewer interface {
	Release(ctx context.Context, tenantID, id, reviewedBy, note string) (*FilteredMessage, error)
	Reject(ctx context.Context, tenantID, id, reviewedBy, note string) (*FilteredMessage, error)
}

type reviewer struct {
	quarantine QuarantineRepository
	outbound   OutboundReleaser
	inbound    InboundReleaser
}

// NewReviewer wires the queue to the two pipelines it can return a message to.
// Either releaser may be nil, which makes releasing in that direction an error
// rather than a silent no-op — a reviewer who is told a message went out has
// to be right.
func NewReviewer(quarantine QuarantineRepository, outbound OutboundReleaser, inbound InboundReleaser) Reviewer {
	return &reviewer{quarantine: quarantine, outbound: outbound, inbound: inbound}
}

// Release sends a held message on its way.
//
// The status is claimed before the message moves, and that order is the whole
// of the concurrency safety here. Review is one conditional UPDATE that only
// touches a PENDING row, so of two reviewers pressing release at the same
// moment exactly one wins and the loser is told so. Releasing first and
// recording afterwards would let both win the release and send the message
// twice, which is the one outcome a review queue must never produce.
//
// The cost of this order is the opposite failure: if the payload cannot be
// moved after the status is claimed, the message reads as released but has not
// gone. That is visible, it is logged loudly, and a human can send it again —
// where a double send cannot be taken back.
func (r *reviewer) Release(ctx context.Context, tenantID, id, reviewedBy, note string) (*FilteredMessage, error) {
	record, err := r.quarantine.Review(ctx, tenantID, id, StatusReleased, reviewedBy, note)
	if err != nil {
		return nil, err
	}

	if err := r.deliver(ctx, record); err != nil {
		slog.Error("a released message was marked released but could not be sent",
			"error", err, "id", record.ID, "tenant_id", tenantID,
			"message_id", record.MessageID, "direction", record.Direction)
		return record, fmt.Errorf("emailfilter: %s was marked released but not delivered: %w", id, err)
	}

	slog.Info("filtered message released",
		"id", record.ID, "tenant_id", tenantID, "by", reviewedBy, "direction", record.Direction)
	return record, nil
}

func (r *reviewer) deliver(ctx context.Context, record *FilteredMessage) error {
	// Only a hold is waiting for anything. A tag was delivered when it was
	// recorded and a reject never had a payload, so "releasing" either is a
	// status change and nothing more.
	if record.Action != ActionHold {
		return nil
	}
	if record.PayloadRef == "" {
		return fmt.Errorf("no payload was retained")
	}

	switch record.Direction {
	case DirectionOutbound:
		if r.outbound == nil {
			return fmt.Errorf("outbound release is not configured")
		}
		return r.outbound.ReleaseHeld(ctx, record.TenantID, record.PayloadRef)
	case DirectionInbound:
		if r.inbound == nil {
			return fmt.Errorf("inbound release is not configured")
		}
		return r.inbound.ReleaseHeld(ctx, record.TenantID, record.PayloadRef)
	}
	return fmt.Errorf("unknown direction %q", record.Direction)
}

// Reject refuses a held message for good.
//
// Same order as Release and for the same reason: claim the decision first, act
// second. Discarding the payload before the status was claimed would let a
// second reviewer release a message whose bytes had already gone.
func (r *reviewer) Reject(ctx context.Context, tenantID, id, reviewedBy, note string) (*FilteredMessage, error) {
	record, err := r.quarantine.Review(ctx, tenantID, id, StatusRejected, reviewedBy, note)
	if err != nil {
		return nil, err
	}

	if record.Action == ActionHold && record.PayloadRef != "" && record.Direction == DirectionOutbound && r.outbound != nil {
		if err := r.outbound.DiscardHeld(ctx, record.TenantID, record.PayloadRef); err != nil {
			// Not fatal. The decision stands either way, and a held row nobody
			// claims is swept by the outbox's own retention; failing the call
			// here would invite a reviewer to press reject again on a message
			// that is already rejected.
			slog.Error("could not discard the payload of a rejected message",
				"error", err, "id", record.ID, "message_id", record.MessageID)
		}
	}

	slog.Info("filtered message rejected",
		"id", record.ID, "tenant_id", tenantID, "by", reviewedBy, "direction", record.Direction)
	return record, nil
}
