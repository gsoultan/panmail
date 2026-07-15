package pebble

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
)

type store struct {
	db     *pebble.DB
	opChan chan *entities.InboundEmail
	stopCh chan struct{}
	wg     sync.WaitGroup
}

func NewStore(dir string) (stores.InboundRepository, error) {
	opts := &pebble.Options{
		MemTableSize:                64 << 20, // 64MB
		MemTableStopWritesThreshold: 4,
		L0CompactionThreshold:       2,
		L0StopWritesThreshold:       24,
		MaxOpenFiles:                10000,
	}
	db, err := pebble.Open(dir, opts)
	if err != nil {
		return nil, err
	}
	s := &store{
		db:     db,
		opChan: make(chan *entities.InboundEmail, 5000),
		stopCh: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.worker()
	return s, nil
}

func (s *store) worker() {
	defer s.wg.Done()
	batch := s.db.NewBatch()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	count := 0
	for {
		select {
		case e := <-s.opChan:
			_ = s.performWrite(batch, e)
			count++
			if count >= 200 {
				_ = batch.Commit(nil)
				batch = s.db.NewBatch()
				count = 0
			}
		case <-ticker.C:
			if count > 0 {
				_ = batch.Commit(nil)
				batch = s.db.NewBatch()
				count = 0
			}
		case <-s.stopCh:
			if count > 0 {
				_ = batch.Commit(nil)
			}
			return
		}
	}
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

func (s *store) Write(ctx context.Context, e *entities.InboundEmail) error {
	select {
	case s.opChan <- e:
		return nil
	default:
		// Queue full, block if necessary for inbound to avoid losing data
		// Inbound emails are precious
		s.opChan <- e
		return nil
	}
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
		if iter.Valid() && string(iter.Key()) == pageToken {
			iter.Next()
		}
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
	close(s.stopCh)
	s.wg.Wait()
	return s.db.Close()
}
