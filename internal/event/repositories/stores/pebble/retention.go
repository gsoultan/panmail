package pebble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

const (
	eventKeyPrefix     = "events:"
	eventKeyUpperBound = "events;" // ';' is the next byte after ':'

	messageKeyPrefix     = "messages:"
	messageKeyUpperBound = "messages;"

	archiveStamp = "20060102_150405"
	archiveExt   = ".jsonl"

	// Commit in bounded chunks. A single batch covering a store that has been
	// running for a year is a batch that has to fit in memory all at once,
	// during a pass whose whole purpose is that the store got too big.
	truncateBatchSize = 1000
)

// TruncateBefore archives every delivery event older than before and then
// removes it, returning how many events it removed.
func (s *store) TruncateBefore(ctx context.Context, before time.Time) (int64, error) {
	// A zero cutoff would expire the entire store: subtracting an unset time
	// from MaxInt64 overflows, and every key compares as older than it. The
	// retention worker never passes one, but this is an exported delete path
	// and the failure mode is "the store is empty now".
	if before.IsZero() {
		return 0, errors.New("retention: refusing to prune with a zero cutoff")
	}

	// Event keys carry a *descending* timestamp, so "older than before" is a
	// key component greater than the cutoff. Inverting this comparison deletes
	// everything except what should have gone.
	cutoff := math.MaxInt64 - before.UnixNano()

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(eventKeyPrefix),
		UpperBound: []byte(eventKeyUpperBound),
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	// Archives are written per tenant. A single shared file would let anyone
	// who can download an archive read every other tenant's mail history.
	archives := newTenantArchiveSet(fmt.Sprintf("archive_%s%s", time.Now().Format(archiveStamp), archiveExt))
	defer archives.closeAll()

	batch := s.db.NewBatch()
	var removed int64
	pending := 0

	for iter.SeekGE([]byte(eventKeyPrefix)); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			batch.Close()
			return removed, err
		}

		// events:{tenant_id}:{timestamp_desc}:{id}
		parts := strings.Split(string(iter.Key()), ":")
		if len(parts) < 4 {
			continue
		}
		if ts, _ := strconv.ParseInt(parts[2], 10, 64); ts <= cutoff {
			continue
		}

		rec := expiredEvent{key: iter.Key(), id: parts[3], value: iter.Value()}
		if err := s.expireEvent(batch, archives, rec); err != nil {
			batch.Close()
			return removed, err
		}
		removed++
		pending++

		if pending < truncateBatchSize {
			continue
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			batch.Close()
			return removed, err
		}
		batch = s.db.NewBatch()
		pending = 0
	}

	if err := commitOrClose(batch, pending); err != nil {
		return removed, err
	}
	if removed > 0 {
		compact(s.db, eventRanges)
	}
	return removed, nil
}

// expiredEvent is one event on its way out: the key it lives under, its id,
// and its stored body. The key is carried rather than rebuilt so that deletion
// stays correct for ids the key format cannot round-trip.
type expiredEvent struct {
	key   []byte
	id    string
	value []byte
}

// expireEvent archives one event and stages the deletion of it and of every
// index that points at it.
func (s *store) expireEvent(batch *pebble.Batch, archives *tenantArchiveSet, rec expiredEvent) error {
	var e entities.EmailEvent
	if err := json.Unmarshal(rec.value, &e); err != nil {
		// Unreadable, but still expired: nothing can present it to a user, and
		// keeping it is the unbounded growth this pass exists to stop. Its
		// indexes are keyed by fields only the body holds, so they cannot be
		// cleaned — which is the cost of a row that was already corrupt.
		slog.Warn("retention expired an unreadable event", "key", string(rec.key))
		s.deleteEventRow(batch, rec)
		return nil
	}

	archived, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := archives.write(e.TenantID, archived); err != nil {
		return err
	}

	s.deleteEventIndexes(batch, &e)
	s.deleteEventRow(batch, rec)
	return nil
}

func (s *store) deleteEventRow(batch *pebble.Batch, rec expiredEvent) {
	_ = batch.Delete(rec.key, nil)
	_ = batch.Delete([]byte("event_id:"+rec.id), nil)
}

// deleteEventIndexes removes the message-timeline entry for an event, and the
// latest-event pointer when the event being removed is the one it points at.
func (s *store) deleteEventIndexes(batch *pebble.Batch, e *entities.EmailEvent) {
	if e.MessageID == "" {
		return
	}

	tsDesc := descendingTimestamp(e.Timestamp)
	_ = batch.Delete([]byte(fmt.Sprintf("msg_events:%s:%s:%s:%s:%s",
		e.TenantID, e.MessageID, e.Recipient, tsDesc, e.ID)), nil)

	// latest_events holds one entry per message and recipient. Delete it only
	// when this is the event it points at: dropping it for an older event
	// would hide a message that still has live events from the timeline.
	latestTsKey := []byte(fmt.Sprintf("latest_ts:%s:%s:%s", e.TenantID, e.MessageID, e.Recipient))
	current, closer, err := s.db.Get(latestTsKey)
	if err != nil {
		return
	}
	isLatest := string(current) == tsDesc
	_ = closer.Close()
	if !isLatest {
		return
	}

	_ = batch.Delete([]byte(fmt.Sprintf("latest_events:%s:%s:%s:%s",
		e.TenantID, tsDesc, e.MessageID, e.Recipient)), nil)
	_ = batch.Delete(latestTsKey, nil)
}

// TruncateMessagesBefore removes stored message bodies older than before,
// along with every recipient index entry that points at them.
//
// Unlike events, bodies are deleted outright and never archived. A body is the
// subject, the rendered HTML and every attachment as sent; writing that to a
// file beside the database would answer a request to delete the content with a
// second copy of the content.
func (s *store) TruncateMessagesBefore(ctx context.Context, before time.Time) (int64, error) {
	// A zero cutoff would expire the entire store: subtracting an unset time
	// from MaxInt64 overflows, and every key compares as older than it. The
	// retention worker never passes one, but this is an exported delete path
	// and the failure mode is "the store is empty now".
	if before.IsZero() {
		return 0, errors.New("retention: refusing to prune with a zero cutoff")
	}

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(messageKeyPrefix),
		UpperBound: []byte(messageKeyUpperBound),
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	batch := s.db.NewBatch()
	var removed int64
	pending := 0

	for iter.SeekGE([]byte(messageKeyPrefix)); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			batch.Close()
			return removed, err
		}

		var m entities.EmailMessage
		if err := json.Unmarshal(iter.Value(), &m); err != nil {
			// Skipped rather than deleted, the opposite of an unreadable
			// event: a message's index keys can only be rebuilt from the body,
			// so removing one that will not parse strands its index entries
			// with nothing left to identify them by.
			slog.Warn("retention skipped an unreadable message", "key", string(iter.Key()))
			continue
		}
		if !expired(m.CreatedAt, before) {
			continue
		}

		_ = batch.Delete(iter.Key(), nil)
		for _, idxKey := range recipientMessageKeys(&m) {
			_ = batch.Delete(idxKey, nil)
		}
		removed++
		pending++

		if pending < truncateBatchSize {
			continue
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			batch.Close()
			return removed, err
		}
		batch = s.db.NewBatch()
		pending = 0
	}

	if err := commitOrClose(batch, pending); err != nil {
		return removed, err
	}
	if removed > 0 {
		compact(s.db, messageRanges)
	}
	return removed, nil
}

// PruneArchivesBefore deletes archive files last written before the cutoff.
//
// The comparison is against modification time because that is the date
// ListArchives reports and the archives page shows. An operator setting this
// is reading those dates; pruning on anything else — the stamp in the
// filename, say — makes files disappear that the page said were newer than the
// policy allows.
func (s *store) PruneArchivesBefore(ctx context.Context, before time.Time) (int64, error) {
	// A zero cutoff would expire the entire store: subtracting an unset time
	// from MaxInt64 overflows, and every key compares as older than it. The
	// retention worker never passes one, but this is an exported delete path
	// and the failure mode is "the store is empty now".
	if before.IsZero() {
		return 0, errors.New("retention: refusing to prune with a zero cutoff")
	}

	tenants, err := os.ReadDir(archiveRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var removed int64
	for _, tenant := range tenants {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !tenant.IsDir() {
			continue
		}

		n, err := pruneArchiveDir(filepath.Join(archiveRoot, tenant.Name()), before)
		removed += n
		if err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func pruneArchiveDir(dir string, before time.Time) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var removed int64
	for _, entry := range entries {
		// Only the files this store writes. Anything else an operator has put
		// in the archive directory is not ours to delete.
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), archiveExt) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !info.ModTime().Before(before) {
			continue
		}

		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// expired reports whether a record stamped at createdAt falls before the
// cutoff.
//
// A record with no timestamp is never expired. Every writer sets one, so a
// zero value means a record this code does not understand — and the zero time
// precedes every cutoff, so the permissive reading would delete precisely the
// rows whose age is unknown.
func expired(createdAt, before time.Time) bool {
	if createdAt.IsZero() {
		return false
	}
	return createdAt.Before(before)
}

func commitOrClose(batch *pebble.Batch, pending int) error {
	if pending == 0 {
		return batch.Close()
	}
	return batch.Commit(pebble.Sync)
}
