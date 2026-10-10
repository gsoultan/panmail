package pebble

import (
	"bytes"
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
	// The day family's prefix is the parent of the other two, so its span
	// holds them as well; the walk skips them by these.
	hourDaySeriesRanges = []keyRange{
		{name: "timeseries:hour", lower: hourCounters.prefix, upper: "timeseries:hour;"},
		{name: "timeseries", lower: dayCounters.prefix, upper: "timeseries;"},
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
// hour counters have a retention of their own, PruneHourDaySeriesBefore, which
// is forever unless an administrator sets one.
func (s *store) PruneMinuteSeriesBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("retention: refusing to prune with a zero cutoff")
	}
	p := newPruneBatch(s.db)
	if err := s.pruneCounters(ctx, p, minuteCounters, minuteSeriesRanges[0], nil, before); err != nil {
		return p.abandon(err)
	}
	return p.finish(minuteSeriesRanges)
}

// PruneHourDaySeriesBefore removes hour and day counters for buckets before
// the cutoff, returning how many it removed.
//
// These are what a long chart is drawn from, and they are kept forever unless
// the analytics retention is set (migration 0015 is why that is the default).
// A day bucket goes only once the whole day is before the cutoff, so the day
// the cutoff falls in keeps its counter.
func (s *store) PruneHourDaySeriesBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("retention: refusing to prune with a zero cutoff")
	}
	p := newPruneBatch(s.db)
	if err := s.pruneCounters(ctx, p, hourCounters, hourDaySeriesRanges[0], nil, before); err != nil {
		return p.abandon(err)
	}
	skip := []keyRange{minuteSeriesRanges[0], hourDaySeriesRanges[0]}
	if err := s.pruneCounters(ctx, p, dayCounters, hourDaySeriesRanges[1], skip, before); err != nil {
		return p.abandon(err)
	}
	return p.finish(hourDaySeriesRanges)
}

// pruneCounters stages the deletion of one family's counters for buckets
// before the cutoff. skip names spans inside this one that belong to another
// family, which the walk seeks over.
//
// Walked per tenant: a tenant's keys sort by bucket, so once one is at or
// after the cutoff the rest of that tenant is too, and the walk seeks past it
// rather than reading every live bucket.
func (s *store) pruneCounters(ctx context.Context, p *pruneBatch, family counterFamily, span keyRange, skip []keyRange, before time.Time) error {
	cutoff := bucketTime(before).Format(family.layout)

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(span.lower),
		UpperBound: []byte(span.upper),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

walk:
	for valid := iter.First(); valid; {
		if err := ctx.Err(); err != nil {
			return err
		}

		key := iter.Key()
		for _, other := range skip {
			if bytes.HasPrefix(key, []byte(other.lower)) {
				valid = iter.SeekGE([]byte(other.upper))
				continue walk
			}
		}

		// {prefix}{tenant}:{bucket}:{type}. The hour and minute buckets have
		// a space or a colon of their own, so the bucket is found by its
		// fixed width from the end rather than by splitting.
		tenantPrefix, bucket, ok := splitCounterKey(key, family)
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
			return err
		}
		valid = iter.Next()
	}
	return nil
}

// splitCounterKey returns a counter key's prefix up to and including the
// tenant's separator, and its bucket.
func splitCounterKey(key []byte, family counterFamily) (tenantPrefix []byte, bucket string, ok bool) {
	// The type follows the last colon.
	end := bytes.LastIndexByte(key, ':')
	start := end - len(family.layout)
	if end < 0 || start < len(family.prefix)+1 || key[start-1] != ':' {
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
