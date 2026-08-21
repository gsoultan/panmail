package pebble

import (
	"log/slog"

	"github.com/cockroachdb/pebble"
)

// keyRange is a half-open span of the keyspace, named so a compaction failure
// says which index it was working on.
type keyRange struct {
	name  string
	lower string
	upper string
}

// The spans each prune deletes from. Compaction is per range rather than over
// the whole store so that expiring events does not rewrite the sstables
// holding message bodies, which the same pass may be about to delete anyway.
var (
	eventRanges = []keyRange{
		{name: "events", lower: eventKeyPrefix, upper: eventKeyUpperBound},
		{name: "event_id", lower: "event_id:", upper: "event_id;"},
		{name: "msg_events", lower: "msg_events:", upper: "msg_events;"},
		{name: "latest_events", lower: "latest_events:", upper: "latest_events;"},
		{name: "latest_ts", lower: "latest_ts:", upper: "latest_ts;"},
	}

	messageRanges = []keyRange{
		{name: "messages", lower: messageKeyPrefix, upper: messageKeyUpperBound},
		{name: "recipient_messages", lower: "recipient_messages:", upper: "recipient_messages;"},
	}
)

// compact reclaims the disk a prune freed.
//
// Deleting a key in Pebble writes a tombstone over it; the space comes back
// only when the sstables holding both are rewritten. Without this a retention
// pass makes the store briefly *larger*, and the disk graph an operator set
// the policy to fix never moves — which reads, correctly, as retention not
// working.
//
// **Flush first.** Compact only rewrites sstables, and a tombstone that is
// still in the memtable belongs to no sstable — so compacting without flushing
// rewrites every live key while seeing none of the deletions, does the full
// amount of work, and reclaims nothing. Measured on a 50 MiB store with 90% of
// its rows expired: 50.2 MiB before, 54.0 MiB after. The flush is what makes
// the tombstones visible to the compaction that follows.
//
// Called only when something was actually removed, because this rewrites files
// and there is no point paying for it to find nothing.
//
// Failures are logged rather than returned: the rows are already gone, the
// pass did what it was asked, and reporting it as failed would send an
// operator looking for data that was correctly deleted. The space is reclaimed
// by the next pass, or by Pebble's own compaction in its own time.
func compact(db *pebble.DB, ranges []keyRange) {
	if err := db.Flush(); err != nil {
		slog.Warn("retention could not flush before compacting; the disk will not be reclaimed until later",
			"error", err)
		return
	}

	for _, r := range ranges {
		if err := db.Compact([]byte(r.lower), []byte(r.upper), true); err != nil {
			slog.Warn("retention could not compact after pruning",
				"range", r.name, "error", err)
		}
	}
}
