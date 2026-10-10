package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

// The shared store's own-table retention, for the shadow phase. Asserted here
// so that a method renamed on either side fails the build rather than silently
// leaving the shadow's tables unpruned.
var _ stores.SharedPruner = (*Store)(nil)

// deleteBatch bounds one DELETE. A first pass over a table that has never been
// pruned can match millions of rows, and one statement over all of them holds
// its locks and its undo for as long as that takes.
const deleteBatch = 5000

// DeleteEventsBefore deletes events older than the cutoff from the database
// alone, without archiving them. See stores.SharedPruner: it is for when
// another store holds the same events and archives them itself.
func (s *Store) DeleteEventsBefore(ctx context.Context, before time.Time) (int64, error) {
	conn, err := s.db()
	if err != nil {
		return 0, err
	}

	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		// A subquery with LIMIT rather than DELETE ... LIMIT, which is
		// PostgreSQL syntax nowhere and SQLite syntax only in some builds.
		res, err := conn.ExecContext(ctx,
			`DELETE FROM email_events WHERE id IN (
			   SELECT id FROM email_events WHERE timestamp < $1 LIMIT $2)`,
			before.UTC(), deleteBatch)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < deleteBatch {
			return total, nil
		}
	}
}

// PruneMinuteSeriesBefore deletes per-minute counters for buckets before the
// cutoff, from the database and from the local store, which keeps its own.
func (s *Store) PruneMinuteSeriesBefore(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.local.PruneMinuteSeriesBefore(ctx, before)
	logMirrorPrune("minute_series", n)
	if err != nil {
		return 0, fmt.Errorf("local store: %w", err)
	}
	return s.DeleteMinuteSeriesBefore(ctx, before)
}

// DeleteMinuteSeriesBefore deletes per-minute counters for buckets before the
// cutoff from the database alone.
//
// Minute rows only. Day and hour rows are what a long chart is drawn from, and
// migration 0015 exists because losing them emptied it; they are kept for
// good. The bucket is ISO-ordered text in UTC, so formatting the cutoff the
// same way makes a string comparison a time comparison.
func (s *Store) DeleteMinuteSeriesBefore(ctx context.Context, before time.Time) (int64, error) {
	conn, err := s.db()
	if err != nil {
		return 0, err
	}
	_, layout := timeseriesFamily("minute")
	res, err := conn.ExecContext(ctx,
		`DELETE FROM email_event_timeseries WHERE granularity = $1 AND bucket < $2`,
		"minute", before.UTC().Format(layout))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneResourceHistoryBefore is local: resource samples describe this process
// and are only ever kept in the local store.
func (s *Store) PruneResourceHistoryBefore(ctx context.Context, before time.Time) (int64, error) {
	return s.local.PruneResourceHistoryBefore(ctx, before)
}

// logMirrorPrune reports what retention removed from the local store.
//
// Logged rather than counted: the same events and bodies are in the database,
// whose count is the one returned, and adding the two would report every
// expired event twice.
func logMirrorPrune(class string, removed int64) {
	if removed > 0 {
		slog.Info("retention pruned the local mirror of the shared store",
			"class", class, "removed", removed)
	}
}
