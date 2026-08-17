package pebble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
)

const (
	inboundKeyPrefix     = "inbound:"
	inboundKeyUpperBound = "inbound;" // ';' is the next byte after ':', and sorts below the '_' of inbound_idx

	truncateBatchSize = 500
)

// TruncateBefore removes received mail stored before the cutoff, along with
// its id index, and returns how many messages it removed.
//
// This is the one retention pass that destroys the only copy panmail holds of
// something a person was sent, which is why the default is to keep inbound
// mail forever and why a message that cannot be read back is left alone rather
// than swept up with the rest.
func (s *store) TruncateBefore(ctx context.Context, before time.Time) (int64, error) {
	// A zero cutoff would expire the entire store: subtracting an unset time
	// from MaxInt64 overflows, and every key compares as older than it. The
	// retention worker never passes one, but this is an exported delete path
	// and the failure mode is "the store is empty now".
	if before.IsZero() {
		return 0, errors.New("retention: refusing to prune with a zero cutoff")
	}

	// Keys carry a descending timestamp, so older mail sorts *after* the
	// cutoff, not before it.
	cutoff := math.MaxInt64 - before.UnixNano()

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(inboundKeyPrefix),
		UpperBound: []byte(inboundKeyUpperBound),
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	batch := s.db.NewBatch()
	var removed int64
	pending := 0

	for iter.SeekGE([]byte(inboundKeyPrefix)); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			batch.Close()
			return removed, err
		}

		// inbound:{tenant_id}:{timestamp_desc}:{id}
		parts := strings.Split(string(iter.Key()), ":")
		if len(parts) < 4 {
			continue
		}
		if ts, _ := strconv.ParseInt(parts[2], 10, 64); ts <= cutoff {
			continue
		}

		var e entities.InboundEmail
		if err := json.Unmarshal(iter.Value(), &e); err != nil {
			slog.Warn("retention skipped an unreadable inbound message", "key", string(iter.Key()))
			continue
		}

		_ = batch.Delete(iter.Key(), nil)
		_ = batch.Delete([]byte(fmt.Sprintf("inbound_idx:%s:%s", e.TenantID, e.ID)), nil)
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
		s.compact()
	}
	return removed, nil
}

func commitOrClose(batch *pebble.Batch, pending int) error {
	if pending == 0 {
		return batch.Close()
	}
	return batch.Commit(pebble.Sync)
}

// compact reclaims the disk a prune freed.
//
// Deleting a key in Pebble writes a tombstone over it; the space comes back
// only when the sstables holding both are rewritten. Without this, expiring a
// year of received mail leaves the store the size it was, which is the one
// thing an operator sets an inbound retention to change.
//
// The flush is not optional. Compact only rewrites sstables, and a tombstone
// still in the memtable belongs to none of them — so compacting without
// flushing first rewrites every live key while seeing no deletions at all,
// paying the full cost and reclaiming nothing.
//
// Failures are logged rather than returned. The mail is already deleted, and
// reporting the pass as failed would send someone looking for it.
func (s *store) compact() {
	if err := s.db.Flush(); err != nil {
		slog.Warn("retention could not flush the inbound store; the disk will not be reclaimed until later",
			"error", err)
		return
	}

	for _, r := range [][2]string{
		{inboundKeyPrefix, inboundKeyUpperBound},
		{"inbound_idx:", "inbound_idx;"},
	} {
		if err := s.db.Compact([]byte(r[0]), []byte(r[1]), true); err != nil {
			slog.Warn("retention could not compact inbound store after pruning",
				"range", r[0], "error", err)
		}
	}
}
