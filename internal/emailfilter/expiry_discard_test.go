package emailfilter_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/emailfilter"
)

// discardRecorder is the outbox side of a hold: it records what it was asked
// to discard and prune.
type discardRecorder struct {
	discarded  []string
	discardErr error

	prunedBefore time.Time
	pruned       int64
	pruneErr     error
}

func (d *discardRecorder) ReleaseHeld(context.Context, string, string) error { return nil }

func (d *discardRecorder) DiscardHeld(_ context.Context, _, messageID string) error {
	d.discarded = append(d.discarded, messageID)
	return d.discardErr
}

func (d *discardRecorder) PruneHeld(_ context.Context, before time.Time) (int64, error) {
	d.prunedBefore = before
	return d.pruned, d.pruneErr
}

// An expired hold can never be released, but its payload -- the whole request,
// attachments included -- stayed in the outbox for good: the sweep flipped the
// review record and nothing else, and the outbox's own retention only prunes
// FAILED rows.
func TestAnExpiredOutboundHoldHasItsPayloadDiscarded(t *testing.T) {
	outboundHold := expiredRecord("q1", "t1")
	outboundHold.PayloadRef = "m-q1"
	inbound := expiredRecord("q2", "t1")
	inbound.Direction, inbound.PayloadRef = emailfilter.DirectionInbound, "in-q2"
	noPayload := expiredRecord("q3", "t1")

	q := &stubExpiry{records: []emailfilter.FilteredMessage{outboundHold, inbound, noPayload}}
	outbox := &discardRecorder{}

	count, err := emailfilter.NewExpirySweeper(q, nil, outbox).Expire(context.Background(), time.Now(), 500)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	if !slices.Equal(outbox.discarded, []string{"m-q1"}) {
		t.Errorf("discarded %v, want only the outbound hold's payload", outbox.discarded)
	}
}

// The expiry stands whether or not the payload could be discarded; the prune
// is the backstop for the payload.
func TestAFailedDiscardDoesNotFailTheExpiry(t *testing.T) {
	record := expiredRecord("q1", "t1")
	record.PayloadRef = "m-q1"
	q := &stubExpiry{records: []emailfilter.FilteredMessage{record}}
	n := &countingNotifier{}
	outbox := &discardRecorder{discardErr: errors.New("database unreachable")}

	count, err := emailfilter.NewExpirySweeper(q, n, outbox).Expire(context.Background(), time.Now(), 500)
	if err != nil || count != 1 {
		t.Fatalf("Expire = %d, %v; want the expiry to stand", count, err)
	}
	if len(n.expired) != 1 {
		t.Errorf("announced %d expiries, want 1", len(n.expired))
	}
}

// PruneBefore is the retention entry point: the review history, and the held
// payloads nothing discarded, on one cutoff.
func TestPruneBeforePrunesHistoryAndHeldPayloads(t *testing.T) {
	q := &stubExpiry{pruned: 4}
	outbox := &discardRecorder{pruned: 2}
	before := time.Now().Add(-30 * 24 * time.Hour)

	removed, err := emailfilter.NewExpirySweeper(q, nil, outbox).PruneBefore(context.Background(), before)
	if err != nil {
		t.Fatalf("PruneBefore: %v", err)
	}
	if removed != 6 {
		t.Errorf("removed = %d, want both prunes counted", removed)
	}
	if !outbox.prunedBefore.Equal(before) {
		t.Errorf("held payloads pruned before %v, want %v", outbox.prunedBefore, before)
	}

	// One failing does not hide the other's count, or its error.
	q.pruneErr = errors.New("history unreachable")
	removed, err = emailfilter.NewExpirySweeper(q, nil, outbox).PruneBefore(context.Background(), before)
	if err == nil {
		t.Error("a failed history prune was reported as success")
	}
	if removed != 6 {
		t.Errorf("removed = %d, want what was removed despite the failure", removed)
	}
}
