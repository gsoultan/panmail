package pebble

import (
	"bytes"
	"time"

	"github.com/cockroachdb/pebble"
)

// The layouts of the three counter families. ISO-ordered, so a bucket sorts as
// text, which is what lets a range of them be read as a key range.
const (
	dayLayout    = "2006-01-02"
	hourLayout   = "2006-01-02 15"
	minuteLayout = "2006-01-02 15:04"
)

// bucketTime is the instant a counter bucket is named from: always UTC.
//
// The writer used to format the event's own time.Now(), which is the server's
// local zone, while every caller asks in UTC and the shared store's writer
// buckets in UTC. On a server east of Greenwich a day's chart took its first
// hours from the previous day and lost its last ones to the next.
//
// Counters written before this change are named in whatever zone the server
// ran in. They are left as they are: counters are sums, and re-bucketing them
// would mean splitting each local hour or day across UTC ones with no record
// of where in it the events fell. On a server that ran in UTC nothing moves.
// Anywhere else, the day and hour buckets written before the upgrade stay
// offset by the server's UTC offset — a one-off skew in history that ends at
// the upgrade, and the reason a chart spanning it can show a step.
func bucketTime(at time.Time) time.Time {
	return at.UTC()
}

// counterFamily is one of the three bucket sizes the writer keeps.
type counterFamily struct {
	prefix string // the key prefix before the tenant
	layout string
	step   time.Duration
}

var (
	dayCounters    = counterFamily{prefix: "timeseries:", layout: dayLayout, step: 24 * time.Hour}
	hourCounters   = counterFamily{prefix: "timeseries:hour:", layout: hourLayout, step: time.Hour}
	minuteCounters = counterFamily{prefix: "timeseries:minute:", layout: minuteLayout, step: time.Minute}
)

// rangeCounters totals each event type over [start, end], at minute
// resolution, from the coarsest counters that tile the range.
//
// It used to add up every minute counter in the range, which for the
// dashboard's ninety-day view is 129,600 minutes times every event type —
// 648,000 keys and 811 ms on every load. Every event bumps its day, hour and
// minute counter in the same batch, so a whole day's counter equals the sum of
// its minutes; reading the day instead is the same answer from one key. Only
// the ragged edges need finer counters: the minutes up to the first hour
// boundary, the hours up to the first day boundary, and the same at the other
// end. A range is at most five key spans whatever its length.
//
// The answer is identical to the minute sum by construction, including its
// boundaries: both ends are truncated to the minute, and the end's minute is
// included, exactly as the minute range read did.
//
// One difference survives, and it is deliberate: minute counters are pruned
// after a horizon while day and hour counters are kept for good. A ragged edge
// older than that horizon loses the minutes of its partial hour — a few events
// at the edge of a months-long range, where the minute sum would have lost the
// whole of it.
func (s *store) rangeCounters(tenantID string, start, end time.Time) (map[string]int64, error) {
	cur := bucketTime(start).Truncate(time.Minute)
	last := bucketTime(end).Truncate(time.Minute)

	res := make(map[string]int64)
	for !cur.After(last) {
		// What is left of the range, in whole minutes including the last one.
		remaining := last.Sub(cur) + time.Minute

		family := minuteCounters
		var boundary time.Time
		switch {
		case cur.Hour() == 0 && cur.Minute() == 0 && remaining >= 24*time.Hour:
			family = dayCounters
		case cur.Minute() == 0 && remaining >= time.Hour:
			family = hourCounters
			boundary = time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, time.UTC)
		default:
			boundary = cur.Truncate(time.Hour).Add(time.Hour)
		}

		// As many whole buckets as fit in what is left, stopping at the next
		// boundary of the coarser family so the run after it can use that.
		n := remaining / family.step
		if !boundary.IsZero() {
			n = min(n, boundary.Sub(cur)/family.step)
		}
		through := cur.Add((n - 1) * family.step)

		if err := s.sumCounters(res, family, tenantID, cur, through); err != nil {
			return nil, err
		}
		cur = through.Add(family.step)
	}
	return res, nil
}

// sumCounters adds every counter of one family whose bucket lies in
// [from, through] into res, by event type.
func (s *store) sumCounters(res map[string]int64, family counterFamily, tenantID string, from, through time.Time) error {
	base := family.prefix + tenantID + ":"
	lower := []byte(base + from.Format(family.layout))
	upper := []byte(base + through.Format(family.layout) + "\xff")

	iter, err := s.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		i := bytes.LastIndexByte(key, ':')
		if i < 0 {
			continue
		}
		res[string(key[i+1:])] += int64(decodeUint64(iter.Value()))
	}
	return nil
}
