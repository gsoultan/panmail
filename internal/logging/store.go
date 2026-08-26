package logging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/google/uuid"
	"github.com/gsoultan/panmail/pkg/pebbleopt"
)

type LogEntry struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	Timestamp time.Time         `json:"timestamp"`
	Level     string            `json:"level"`
	Message   string            `json:"message"`
	Service   string            `json:"service"`
	Metadata  map[string]string `json:"metadata"`
}

type Store interface {
	Write(entry LogEntry) error
	List(pageSize int, pageToken string, tenantID string) ([]LogEntry, string, error)
	Subscribe(ctx context.Context) <-chan LogEntry
	TruncateBefore(ctx context.Context, before time.Time) (int64, error)
	Close() error
}

type pebbleStore struct {
	db          *pebble.DB
	subscribers sync.Map
	logChan     chan LogEntry
	stopChan    chan struct{}
	wg          sync.WaitGroup
	closed      sync.Once
}

func NewPebbleStore(dir string) (Store, error) {
	opts := &pebble.Options{
		MemTableSize:                pebbleopt.MemTableSize(),
		MemTableStopWritesThreshold: 4,
		L0CompactionThreshold:       2,
		L0StopWritesThreshold:       24,
		MaxOpenFiles:                10000,
	}
	db, err := pebble.Open(dir, opts)
	if err != nil {
		return nil, err
	}
	s := &pebbleStore{
		db:       db,
		logChan:  make(chan LogEntry, 10000), // Large buffer for logs
		stopChan: make(chan struct{}),
	}

	// Start background worker for async writes
	s.wg.Add(1)
	go s.worker()

	return s, nil
}

func (s *pebbleStore) worker() {
	defer s.wg.Done()
	batch := s.db.NewBatch()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	count := 0
	for {
		select {
		case entry := <-s.logChan:
			// Key: timestamp + ID for uniqueness and sorting
			key := []byte(fmt.Sprintf("%d_%s", entry.Timestamp.UnixNano(), entry.ID))
			val, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			_ = batch.Set(key, val, nil)
			count++

			// Commit if batch gets too big
			if count >= 100 {
				_ = batch.Commit(nil)
				batch = s.db.NewBatch()
				count = 0
			}

		case <-ticker.C:
			// Commit periodically even if batch is small
			if count > 0 {
				_ = batch.Commit(nil)
				batch = s.db.NewBatch()
				count = 0
			}

		case <-s.stopChan:
			// Drain remaining logs
			for {
				select {
				case entry := <-s.logChan:
					key := []byte(fmt.Sprintf("%d_%s", entry.Timestamp.UnixNano(), entry.ID))
					val, _ := json.Marshal(entry)
					_ = batch.Set(key, val, nil)
					count++
					if count >= 100 {
						_ = batch.Commit(nil)
						batch = s.db.NewBatch()
						count = 0
					}
				default:
					if count > 0 {
						_ = batch.Commit(nil)
					} else {
						batch.Close()
					}
					return
				}
			}
		}
	}
}

func (s *pebbleStore) Write(entry LogEntry) error {
	// Broadcast to subscribers immediately (synchronous for live view)
	s.subscribers.Range(func(k, v any) bool {
		ch := v.(chan LogEntry)
		select {
		case ch <- entry:
		default:
		}
		return true
	})

	// Async write to DB
	select {
	case <-s.stopChan:
		return nil
	case s.logChan <- entry:
		return nil
	default:
		// Channel full, drop log entry to avoid blocking main flow
		return nil
	}
}

func (s *pebbleStore) Subscribe(ctx context.Context) <-chan LogEntry {
	ch := make(chan LogEntry, 100)
	id := uuid.New().String()
	s.subscribers.Store(id, ch)

	go func() {
		<-ctx.Done()
		s.subscribers.Delete(id)
		close(ch)
	}()

	return ch
}

func (s *pebbleStore) List(pageSize int, pageToken string, tenantID string) ([]LogEntry, string, error) {
	var entries []LogEntry
	iter, err := s.db.NewIter(&pebble.IterOptions{})
	if err != nil {
		return nil, "", err
	}
	defer iter.Close()

	if pageToken != "" {
		iter.SeekGE([]byte(pageToken))
	} else {
		iter.Last() // Start from most recent if no token
	}

	count := 0
	for ; iter.Valid() && count < pageSize; iter.Prev() {
		var entry LogEntry
		if err := json.Unmarshal(iter.Value(), &entry); err != nil {
			continue
		}

		// Filter by tenant if requested
		if tenantID != "" && entry.TenantID != "" && entry.TenantID != tenantID {
			continue
		}

		entries = append(entries, entry)
		count++
	}

	nextPageToken := ""
	if iter.Valid() {
		nextPageToken = string(iter.Key())
	}

	return entries, nextPageToken, nil
}

// TruncateBefore removes log entries written before the cutoff, returning how
// many it removed.
//
// Log keys are "{unix_nano}_{id}", so the cutoff is a key prefix and the scan
// is bounded by it rather than by reading every entry: an upper bound of
// "{cutoff_nano}_" stops the iterator at the first entry worth keeping. The
// timestamp is parsed back out anyway, because that bound is only exact while
// every key has the same digit width, and a store that outlives that
// assumption should keep data rather than guess.
func (s *pebbleStore) TruncateBefore(ctx context.Context, before time.Time) (int64, error) {
	// A zero cutoff would expire the entire store: subtracting an unset time
	// from MaxInt64 overflows, and every key compares as older than it. The
	// retention worker never passes one, but this is an exported delete path
	// and the failure mode is "the store is empty now".
	if before.IsZero() {
		return 0, errors.New("retention: refusing to prune with a zero cutoff")
	}

	cutoff := before.UnixNano()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		UpperBound: []byte(fmt.Sprintf("%d_", cutoff)),
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	batch := s.db.NewBatch()
	var removed int64
	pending := 0

	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			batch.Close()
			return removed, err
		}

		stamp, _, found := strings.Cut(string(iter.Key()), "_")
		if !found {
			continue
		}
		at, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || at >= cutoff {
			continue
		}

		_ = batch.Delete(iter.Key(), nil)
		removed++
		pending++

		if pending < logTruncateBatchSize {
			continue
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			batch.Close()
			return removed, err
		}
		batch = s.db.NewBatch()
		pending = 0
	}

	if pending == 0 {
		if err := batch.Close(); err != nil {
			return removed, err
		}
	} else if err := batch.Commit(pebble.Sync); err != nil {
		return removed, err
	}

	if removed > 0 {
		// Deleting a key in Pebble writes a tombstone over it; the space comes
		// back only when the sstables holding both are rewritten. Log volume
		// is the reason this store grows fastest of the three, so a retention
		// that does not reclaim is the one nobody would notice was broken.
		//
		// Flush first: Compact only rewrites sstables, and a tombstone still
		// in the memtable belongs to none of them, so compacting without
		// flushing rewrites every live key while seeing no deletions.
		//
		// Only the expired span, and only when something went. Failures are
		// logged rather than returned: the entries are already deleted.
		if err := s.db.Flush(); err != nil {
			slog.Warn("retention could not flush the log store; the disk will not be reclaimed until later",
				"error", err)
			return removed, nil
		}
		if err := s.db.Compact([]byte{0}, []byte(fmt.Sprintf("%d_", cutoff)), true); err != nil {
			slog.Warn("retention could not compact log store after pruning", "error", err)
		}
	}
	return removed, nil
}

const logTruncateBatchSize = 1000

func (s *pebbleStore) Close() error {
	var err error
	s.closed.Do(func() {
		close(s.stopChan)
		s.wg.Wait()
		err = s.db.Close()
	})
	return err
}

// Checkpoint writes a consistent snapshot of the log store into dir. A
// filesystem copy of a live store silently omits unflushed writes.
func (s *pebbleStore) Checkpoint(dir string) error {
	return s.db.Checkpoint(dir)
}
