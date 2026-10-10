package pebble

import "time"

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
