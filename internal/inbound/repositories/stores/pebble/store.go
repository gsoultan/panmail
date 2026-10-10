package pebble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
	"github.com/gsoultan/panmail/pkg/pebbleopt"
)

// ErrClosed is returned by a write that arrives after Close.
var ErrClosed = errors.New("inbound store is closed")

type store struct {
	db *pebble.DB

	// mu orders writes against Close. Writes hold it shared, so they run
	// concurrently; Close takes it exclusively, so it waits for every write
	// already in progress to commit before the database goes away.
	mu     sync.RWMutex
	closed bool
}

func NewStore(dir string) (stores.InboundRepository, error) {
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
	return &store{db: db}, nil
}

func (s *store) performWrite(batch *pebble.Batch, e *entities.InboundEmail) error {
	timestampDesc := math.MaxInt64 - e.Timestamp.UnixNano()
	tsDescStr := fmt.Sprintf("%019d", timestampDesc)
	key := []byte(fmt.Sprintf("inbound:%s:%s:%s", e.TenantID, tsDescStr, e.ID))
	val, err := json.Marshal(e)
	if err != nil {
		return err
	}

	if err := batch.Set(key, val, nil); err != nil {
		return err
	}

	idxKey := []byte(fmt.Sprintf("inbound_idx:%s:%s", e.TenantID, e.ID))
	return batch.Set(idxKey, []byte(tsDescStr), nil)
}

// Write stores a message and returns once it is committed.
//
// It used to hand the message to a background goroutine and return nil
// straight away. Three things followed from that. A commit error was
// discarded, so a failed write was reported to the caller as a success and
// the message was gone. Close stopped the goroutine without draining its
// queue, so a shutdown lost whatever was queued -- most of it, under load.
// And a GetByID straight after Write missed, which is the check that keeps
// the poller and IDLE from both processing one message: each saw it as new,
// and the bounce was recorded and the tenant's webhook fired twice.
//
// Pebble already groups concurrent synchronous commits into one WAL sync, so
// writing through costs little that the batching goroutine was saving.
func (s *store) Write(ctx context.Context, e *entities.InboundEmail) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrClosed
	}

	batch := s.db.NewBatch()
	defer batch.Close()
	if err := s.performWrite(batch, e); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *store) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.InboundEmail, string, error) {
	prefix := []byte(fmt.Sprintf("inbound:%s:", tenantID))
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	upper[len(upper)-1]++

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	})
	if err != nil {
		return nil, "", err
	}
	defer iter.Close()

	if pageToken != "" {
		iter.SeekGE([]byte(pageToken))
	} else {
		iter.SeekGE(prefix)
	}

	var res []*entities.InboundEmail
	count := 0
	for ; iter.Valid() && count < pageSize; iter.Next() {
		var e entities.InboundEmail
		if err := json.Unmarshal(iter.Value(), &e); err != nil {
			continue
		}
		res = append(res, &e)
		count++
	}

	nextPageToken := ""
	if iter.Valid() {
		nextPageToken = string(iter.Key())
	}

	return res, nextPageToken, nil
}

func (s *store) GetByID(ctx context.Context, tenantID, id string) (*entities.InboundEmail, error) {
	// Pebble store for inbound is optimized for listing. GetByID is harder because key includes timestamp.
	// We can iterate or store a secondary index. For now, since it's a "Gateway" and most use cases are listing/processing,
	// we'll implement a simple scan if needed, but ideally we'd have a secondary index key like "inbound_idx:{tenant_id}:{id}" -> "{timestamp_desc}"

	// Let's implement secondary index for GetByID
	idxKey := []byte(fmt.Sprintf("inbound_idx:%s:%s", tenantID, id))
	tsDesc, closer, err := s.db.Get(idxKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()

	fullKey := []byte(fmt.Sprintf("inbound:%s:%s:%s", tenantID, string(tsDesc), id))
	val, closer2, err := s.db.Get(fullKey)
	if err != nil {
		return nil, err
	}
	defer closer2.Close()

	var e entities.InboundEmail
	if err := json.Unmarshal(val, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *store) Count(ctx context.Context, tenantID string, startTime, endTime time.Time) (int64, error) {
	prefix := []byte(fmt.Sprintf("inbound:%s:", tenantID))
	lower := prefix
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	upper[len(upper)-1]++

	if !endTime.IsZero() {
		tsDesc := math.MaxInt64 - endTime.UnixNano()
		lower = []byte(fmt.Sprintf("inbound:%s:%019d:", tenantID, tsDesc))
	}

	if !startTime.IsZero() {
		tsDesc := math.MaxInt64 - startTime.UnixNano()
		upper = []byte(fmt.Sprintf("inbound:%s:%019d:", tenantID, tsDesc+1))
	}

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: lower,
		UpperBound: upper,
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	var count int64
	for iter.SeekGE(lower); iter.Valid(); iter.Next() {
		count++
	}

	return count, nil
}

func (s *store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// Checkpoint writes a consistent snapshot of this store into dir.
//
// Copying the directory is not equivalent: a live store has writes in
// memtables that have not been flushed and SSTables mid-compaction, so a
// filesystem copy opens cleanly and is quietly missing data. Pebble's
// checkpoint hard-links what it can, so this stays cheap.
func (s *store) Checkpoint(dir string) error {
	return s.db.Checkpoint(dir)
}

// DiskUsage reports how much disk this store is holding, in bytes.
// See the note on the event store's DiskUsage for why this is published.
func (s *store) DiskUsage() int64 {
	if s.db == nil {
		return 0
	}
	return int64(s.db.Metrics().DiskSpaceUsage())
}
