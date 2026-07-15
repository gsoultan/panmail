package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/google/uuid"
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
		if iter.Valid() && string(iter.Key()) == pageToken {
			iter.Prev()
		}
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

func (s *pebbleStore) Close() error {
	var err error
	s.closed.Do(func() {
		close(s.stopChan)
		s.wg.Wait()
		err = s.db.Close()
	})
	return err
}
