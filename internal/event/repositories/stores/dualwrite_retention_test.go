package stores_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

// retentionPrimary is the Pebble store's retention, recorded.
type retentionPrimary struct {
	primaryStub
	pruned []string
}

func (p *retentionPrimary) TruncateBefore(context.Context, time.Time) (int64, error) {
	p.pruned = append(p.pruned, "events")
	return 3, nil
}

func (p *retentionPrimary) TruncateMessagesBefore(context.Context, time.Time) (int64, error) {
	p.pruned = append(p.pruned, "messages")
	return 3, nil
}

func (p *retentionPrimary) PruneMinuteSeriesBefore(context.Context, time.Time) (int64, error) {
	p.pruned = append(p.pruned, "minute_series")
	return 3, nil
}

// prunableShadow is the shared store as the shadow phase sees it: its own
// tables, deleted without archiving.
type prunableShadow struct {
	shadowStub
	deleted []string
	err     error
}

func (s *prunableShadow) DeleteEventsBefore(context.Context, time.Time) (int64, error) {
	s.deleted = append(s.deleted, "events")
	return 3, s.err
}

func (s *prunableShadow) DeleteMessagesBefore(context.Context, time.Time) (int64, error) {
	s.deleted = append(s.deleted, "messages")
	return 3, s.err
}

func (s *prunableShadow) DeleteMinuteSeriesBefore(context.Context, time.Time) (int64, error) {
	s.deleted = append(s.deleted, "minute_series")
	return 3, s.err
}

// The shadow is written for the whole of the shadow phase, so it has to be
// pruned for the whole of it. The wrapper embedded the primary, which answered
// retention alone: email_events and email_messages grew without bound while
// the primary was pruned on schedule.
//
// The primary archives; the shadow's copy is only deleted, or each expired
// event would be archived twice. The count is the primary's.
func TestShadowRetentionReachesBothStores(t *testing.T) {
	cutoff := time.Now().Add(-24 * time.Hour)
	tests := []struct {
		class string
		prune func(stores.EventRepository) (int64, error)
	}{
		{"events", func(r stores.EventRepository) (int64, error) { return r.TruncateBefore(context.Background(), cutoff) }},
		{"messages", func(r stores.EventRepository) (int64, error) {
			return r.TruncateMessagesBefore(context.Background(), cutoff)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.class, func(t *testing.T) {
			primary, shadow := &retentionPrimary{}, &prunableShadow{}
			repo := stores.WithShadow(primary, shadow)

			removed, err := tt.prune(repo)
			if err != nil {
				t.Fatalf("prune: %v", err)
			}
			if removed != 3 {
				t.Errorf("removed = %d; want the primary's count, not both added", removed)
			}
			if !slices.Contains(primary.pruned, tt.class) || !slices.Contains(shadow.deleted, tt.class) {
				t.Errorf("primary pruned %v, shadow deleted %v; want %s in both",
					primary.pruned, shadow.deleted, tt.class)
			}
		})
	}
}

// A shadow that cannot be pruned is reported, unlike a shadow that missed a
// write: a write it missed is a gap in a comparison, a prune it keeps missing
// is a table that fills the disk.
func TestAShadowThatCannotBePrunedIsReported(t *testing.T) {
	primary := &retentionPrimary{}
	shadow := &prunableShadow{err: errors.New("the shared store is unhappy")}
	repo := stores.WithShadow(primary, shadow)

	removed, err := repo.TruncateBefore(context.Background(), time.Now())
	if err == nil {
		t.Fatal("a failed shadow prune reported success")
	}
	if removed != 3 {
		t.Errorf("removed = %d; want the primary's deletions still counted", removed)
	}
}
