package pebble

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/cockroachdb/pebble"
)

var (
	minuteSeriesRanges = []keyRange{
		{name: "timeseries:minute", lower: minuteCounters.prefix, upper: "timeseries:minute;"},
	}
	resourceRanges = []keyRange{
		{name: "resource", lower: "resource:", upper: "resource;"},
	}
)

// PruneMinuteSeriesBefore removes minute counters for buckets before the
// cutoff, returning how many it removed.
//
// Minute counters are the one counter family that grows with time rather than
// with traffic shape: a key per tenant per event type per minute is 7,200 keys
// a day for a tenant using five types, for ever. Nothing reads them at range:
// the dashboard asks for minutes over six hours at most, and a ranged total
// reads them only for the ragged edge of a range (see rangeCounters). Day and
// hour counters are kept for good, deliberately — they are what a long chart
// is drawn from, and migration 0015 exists because losing them emptied it.
//
// Walked per tenant: a tenant's minute keys sort by bucket, so once one is at
// or after the cutoff the rest of that tenant is too, and the walk seeks past
// it rather than reading every live minute.
func (s *store) PruneMinuteSeriesBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("retention: refusing to prune with a zero cutoff")
	}
	cutoff := bucketTime(before).Format(minuteLayout)

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(minuteSeriesRanges[0].lower),
		UpperBound: []byte(minuteSeriesRanges[0].upper),
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	p := newPruneBatch(s.db)
	for valid := iter.First(); valid; {
		if err := ctx.Err(); err != nil {
			return p.abandon(err)
		}

		// timeseries:minute:{tenant}:{yyyy-mm-dd hh:mm}:{type}. The bucket
		// has a colon of its own, so it is found by its fixed width from the
		// end rather than by splitting.
		key := iter.Key()
		tenantPrefix, bucket, ok := splitMinuteKey(key)
		if !ok {
			valid = iter.Next()
			continue
		}
		if bucket >= cutoff {
			// Everything after this for the tenant is newer.
			valid = iter.SeekGE(append(slices.Clone(tenantPrefix), 0xFF))
			continue
		}

		if err := p.delete(key); err != nil {
			return p.abandon(err)
		}
		valid = iter.Next()
	}
	return p.finish(minuteSeriesRanges)
}

// splitMinuteKey returns a minute counter key's prefix up to and including the
// tenant's separator, and its bucket.
func splitMinuteKey(key []byte) (tenantPrefix []byte, bucket string, ok bool) {
	// The type follows the last colon.
	end := len(key) - 1
	for end >= 0 && key[end] != ':' {
		end--
	}
	start := end - len(minuteLayout)
	if start < len(minuteCounters.prefix)+1 || key[start-1] != ':' {
		return nil, "", false
	}
	return key[:start], string(key[start:end]), true
}

// PruneResourceHistoryBefore removes resource samples taken before the cutoff.
//
// One sample every five minutes is small, but it is written for the life of
// the process and nothing reads past the last 24 hours (GetResourceHistory is
// asked for exactly that), so without this the store keeps every sample since
// it was created.
func (s *store) PruneResourceHistoryBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("retention: refusing to prune with a zero cutoff")
	}

	// Descending timestamps, as for events: older samples sort later, so
	// everything from just past the cutoff to the end of the prefix expired.
	from := []byte(fmt.Sprintf("resource:%019d", math.MaxInt64-before.UnixNano()+1))
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: from,
		UpperBound: []byte(resourceRanges[0].upper),
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	p := newPruneBatch(s.db)
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return p.abandon(err)
		}
		if err := p.delete(iter.Key()); err != nil {
			return p.abandon(err)
		}
	}
	return p.finish(resourceRanges)
}

// pruneBatch deletes keys in bounded, synced batches and counts them, which is
// the bookkeeping every prune here repeats.
type pruneBatch struct {
	db      *pebble.DB
	batch   *pebble.Batch
	pending int
	removed int64
}

func newPruneBatch(db *pebble.DB) *pruneBatch {
	return &pruneBatch{db: db, batch: db.NewBatch()}
}

func (p *pruneBatch) delete(key []byte) error {
	if err := p.batch.Delete(key, nil); err != nil {
		return err
	}
	p.removed++
	p.pending++
	if p.pending < truncateBatchSize {
		return nil
	}
	if err := p.batch.Commit(pebble.Sync); err != nil {
		return err
	}
	p.batch = p.db.NewBatch()
	p.pending = 0
	return nil
}

// abandon drops what is staged and reports what was already committed.
func (p *pruneBatch) abandon(err error) (int64, error) {
	_ = p.batch.Close()
	return p.removed - int64(p.pending), err
}

// finish commits the rest and reclaims the space, if anything went.
func (p *pruneBatch) finish(ranges []keyRange) (int64, error) {
	if err := commitOrClose(p.batch, p.pending); err != nil {
		return p.removed - int64(p.pending), err
	}
	if p.removed > 0 {
		compact(p.db, ranges)
	}
	return p.removed, nil
}
