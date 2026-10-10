package pebble

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
	"github.com/gsoultan/panmail/pkg/pebbleopt"
)

type store struct {
	db      *pebble.DB
	bufPool sync.Pool
	opChan  chan any
	stopCh  chan struct{}
	wg      sync.WaitGroup

	// closeMu orders enqueueing against Close. A sender holds it for reading
	// across its check of closed and its send, so once Close has taken it for
	// writing and set closed, no op can reach opChan behind the worker's final
	// drain — which is what lets that drain be the last word.
	closeMu   sync.RWMutex
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

// ErrClosed is returned by a write that arrives after Close. The write is
// refused rather than queued, because a queued op nobody will ever commit is a
// silent loss, and blocking on a channel nobody reads would hang the caller.
var ErrClosed = errors.New("event store is closed")

type writeOp struct {
	event *entities.EmailEvent
}

type messageOp struct {
	message *entities.EmailMessage
}

type resourceOp struct {
	point entities.ResourcePoint
}

// latestExpiryOp asks the writer to drop latest-event pointers for events
// retention has expired, each only if it still points at that event.
//
// Retention used to do this itself, and raced the writer: it read latest_ts,
// the writer then moved the pointer to a newer event, and retention's commit
// deleted latest_ts regardless — leaving the newer latest_events entry with
// nothing pointing at it, so the next write for that message and recipient
// added a second one and the timeline listed the message twice, forever.
// latest_ts is now read and written by one goroutine only, which removes the
// race rather than narrowing it.
type latestExpiryOp struct {
	expired []latestPointer
	// done is closed once the deletions are committed, so the retention pass
	// can compact over them.
	done chan struct{}
}

// latestPointer names one latest_events entry by what its key is built from.
type latestPointer struct {
	tenantID, messageID, recipient, tsDesc string
}

func NewStore(dir string) (stores.EventRepository, error) {
	opts := &pebble.Options{
		Merger: &pebble.Merger{
			Name: "pebble.concatenate", // Use default name for compatibility with existing DBs
			Merge: func(key, value []byte) (pebble.ValueMerger, error) {
				return &counterValueMerger{sum: decodeUint64(value)}, nil
			},
		},
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
	s := &store{
		db: db,
		bufPool: sync.Pool{
			New: func() any {
				return make([]byte, 0, 128)
			},
		},
		opChan: make(chan any, 20000),
		stopCh: make(chan struct{}),
	}

	s.wg.Add(1)
	go s.worker()

	return s, nil
}

type counterValueMerger struct {
	sum uint64
}

func (c *counterValueMerger) MergeNewer(value []byte) error {
	c.sum += decodeUint64(value)
	return nil
}

func (c *counterValueMerger) MergeOlder(value []byte) error {
	c.sum += decodeUint64(value)
	return nil
}

func (c *counterValueMerger) Finish(includesBase bool) ([]byte, io.Closer, error) {
	res := make([]byte, 8)
	binary.LittleEndian.PutUint64(res, c.sum)
	return res, nil, nil
}

func decodeUint64(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}

func (s *store) worker() {
	defer s.wg.Done()
	batch := s.db.NewBatch()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// Cache to track latest timestamps within the current batch
	// Key: tenantID + ":" + messageID
	batchLatestTs := make(map[string]string)

	count := 0
	commit := func() {
		if count == 0 {
			return
		}
		_ = batch.Commit(nil)
		batch = s.db.NewBatch()
		clear(batchLatestTs)
		count = 0
	}
	apply := func(op any) {
		switch v := op.(type) {
		case writeOp:
			_ = s.performWrite(batch, v.event, batchLatestTs)
		case messageOp:
			_ = s.performWriteMessage(batch, v.message)
		case resourceOp:
			_ = s.performWriteResource(batch, v.point)
		case latestExpiryOp:
			s.performLatestExpiry(batch, v.expired, batchLatestTs)
			// Committed at once rather than with the next batch: the pass
			// that sent this compacts over these keys as soon as it returns.
			count++
			commit()
			close(v.done)
			return
		}
		count++
		if count >= 500 {
			commit()
		}
	}

	for {
		select {
		case op := <-s.opChan:
			apply(op)
		case <-ticker.C:
			commit()
		case <-s.stopCh:
			// Drain before the final commit. select picks among ready cases at
			// random, so stop can win while thousands of ops are still
			// buffered — and every one of them is a write its caller was told
			// had been accepted. Close has already stopped new ops arriving,
			// so this terminates.
			for {
				select {
				case op := <-s.opChan:
					apply(op)
					continue
				default:
				}
				break
			}
			commit()
			_ = batch.Close()
			return
		}
	}
}

// enqueue hands an op to the writer, or refuses it once the store is closing.
func (s *store) enqueue(ctx context.Context, op any) error {
	s.closeMu.RLock()
	defer s.closeMu.RUnlock()
	if s.closed {
		return ErrClosed
	}
	select {
	case s.opChan <- op:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *store) performWrite(batch *pebble.Batch, e *entities.EmailEvent, batchLatestTs map[string]string) error {
	// Retention rebuilds these same index keys to delete them, so both sides
	// go through descendingTimestamp.
	tsDescStr := descendingTimestamp(e.Timestamp)

	buf := s.bufPool.Get().([]byte)
	defer func() {
		buf = buf[:0]
		s.bufPool.Put(buf)
	}()

	// events:{tenant_id}:{timestamp_desc}:{id}
	buf = append(buf, "events:"...)
	buf = append(buf, e.TenantID...)
	buf = append(buf, ':')
	buf = append(buf, tsDescStr...)
	buf = append(buf, ':')
	buf = append(buf, e.ID...)
	key := make([]byte, len(buf))
	copy(key, buf)

	val, err := json.Marshal(e)
	if err != nil {
		return err
	}

	typeStr := strings.TrimPrefix(e.Type.String(), "EMAIL_EVENT_TYPE_")

	// Total metrics
	buf = buf[:0]
	buf = append(buf, "metrics:"...)
	buf = append(buf, e.TenantID...)
	buf = append(buf, ':')
	buf = append(buf, typeStr...)
	metricKey := make([]byte, len(buf))
	copy(metricKey, buf)
	_ = s.incrementCounter(batch, metricKey)

	// Daily timeseries
	dateStr := e.Timestamp.Format("2006-01-02")
	buf = buf[:0]
	buf = append(buf, "timeseries:"...)
	buf = append(buf, e.TenantID...)
	buf = append(buf, ':')
	buf = append(buf, dateStr...)
	buf = append(buf, ':')
	buf = append(buf, typeStr...)
	tsKey := make([]byte, len(buf))
	copy(tsKey, buf)
	_ = s.incrementCounter(batch, tsKey)

	// Hourly timeseries
	hourStr := e.Timestamp.Format("2006-01-02 15")
	buf = buf[:0]
	buf = append(buf, "timeseries:hour:"...)
	buf = append(buf, e.TenantID...)
	buf = append(buf, ':')
	buf = append(buf, hourStr...)
	buf = append(buf, ':')
	buf = append(buf, typeStr...)
	hourKey := make([]byte, len(buf))
	copy(hourKey, buf)
	_ = s.incrementCounter(batch, hourKey)

	// Minute timeseries
	minuteStr := e.Timestamp.Format("2006-01-02 15:04")
	buf = buf[:0]
	buf = append(buf, "timeseries:minute:"...)
	buf = append(buf, e.TenantID...)
	buf = append(buf, ':')
	buf = append(buf, minuteStr...)
	buf = append(buf, ':')
	buf = append(buf, typeStr...)
	minuteKey := make([]byte, len(buf))
	copy(minuteKey, buf)
	_ = s.incrementCounter(batch, minuteKey)

	_ = batch.Set(key, val, nil)

	// event_id index
	buf = buf[:0]
	buf = append(buf, "event_id:"...)
	buf = append(buf, e.ID...)
	idKey := make([]byte, len(buf))
	copy(idKey, buf)

	buf = buf[:0]
	buf = append(buf, e.TenantID...)
	buf = append(buf, ':')
	buf = append(buf, tsDescStr...)
	idVal := make([]byte, len(buf))
	copy(idVal, buf)

	_ = batch.Set(idKey, idVal, nil)

	// msg_events index: msg_events:{tenant_id}:{message_id}:{recipient}:{timestamp_desc}:{event_id}
	if e.MessageID != "" {
		buf = buf[:0]
		buf = append(buf, "msg_events:"...)
		buf = append(buf, e.TenantID...)
		buf = append(buf, ':')
		buf = append(buf, e.MessageID...)
		buf = append(buf, ':')
		buf = append(buf, e.Recipient...)
		buf = append(buf, ':')
		buf = append(buf, tsDescStr...)
		buf = append(buf, ':')
		buf = append(buf, e.ID...)
		msgEventKey := make([]byte, len(buf))
		copy(msgEventKey, buf)
		_ = batch.Set(msgEventKey, val, nil)

		// Latest events index: latest_events:{tenant_id}:{timestamp_desc}:{message_id}:{recipient}
		// 1. Check if there is an existing latest event for this message + recipient
		latestTsKey := []byte(fmt.Sprintf("latest_ts:%s:%s:%s", e.TenantID, e.MessageID, e.Recipient))
		cacheKey := e.TenantID + ":" + e.MessageID + ":" + e.Recipient
		oldTsStr := ""

		if ts, ok := batchLatestTs[cacheKey]; ok {
			oldTsStr = ts
		} else if oldTsVal, closer, err := s.db.Get(latestTsKey); err == nil {
			oldTsStr = string(oldTsVal)
			_ = closer.Close()
		}

		if oldTsStr == "" || tsDescStr <= oldTsStr {
			if oldTsStr != "" {
				// Delete old entry from the time-ordered index
				_ = batch.Delete([]byte(fmt.Sprintf("latest_events:%s:%s:%s:%s", e.TenantID, oldTsStr, e.MessageID, e.Recipient)), nil)
			}

			// 2. Add new entry
			batchLatestTs[cacheKey] = tsDescStr
			_ = batch.Set(latestTsKey, []byte(tsDescStr), nil)

			buf = buf[:0]
			buf = append(buf, "latest_events:"...)
			buf = append(buf, e.TenantID...)
			buf = append(buf, ':')
			buf = append(buf, tsDescStr...)
			buf = append(buf, ':')
			buf = append(buf, e.MessageID...)
			buf = append(buf, ':')
			buf = append(buf, e.Recipient...)
			latestEventKey := make([]byte, len(buf))
			copy(latestEventKey, buf)
			_ = batch.Set(latestEventKey, val, nil)
		}
	}

	return nil
}

// performLatestExpiry drops each latest-event pointer that still names the
// expired event. It consults the batch cache before the database for the same
// reason performWrite does: a newer event may be in this uncommitted batch.
func (s *store) performLatestExpiry(batch *pebble.Batch, expired []latestPointer, batchLatestTs map[string]string) {
	for _, p := range expired {
		cacheKey := p.tenantID + ":" + p.messageID + ":" + p.recipient
		latestTsKey := []byte(fmt.Sprintf("latest_ts:%s:%s:%s", p.tenantID, p.messageID, p.recipient))

		current, ok := batchLatestTs[cacheKey]
		if !ok {
			val, closer, err := s.db.Get(latestTsKey)
			if err != nil {
				continue
			}
			current = string(val)
			_ = closer.Close()
		}
		// Only when this is the event it points at: dropping it for an older
		// event would hide a message that still has live events.
		if current != p.tsDesc {
			continue
		}

		_ = batch.Delete([]byte(fmt.Sprintf("latest_events:%s:%s:%s:%s",
			p.tenantID, p.tsDesc, p.messageID, p.recipient)), nil)
		_ = batch.Delete(latestTsKey, nil)
		// Recorded as "no pointer", which performWrite reads as such, so a
		// write later in this batch does not try to delete it again.
		batchLatestTs[cacheKey] = ""
	}
}

func (s *store) performWriteMessage(batch *pebble.Batch, m *entities.EmailMessage) error {
	val, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_ = batch.Set(messageKey(m.TenantID, m.ID), val, nil)

	// Index by recipient for lookup, newest first. Retention rebuilds these
	// same keys to delete them, so both sides go through recipientMessageKeys.
	for _, idxKey := range recipientMessageKeys(m) {
		_ = batch.Set(idxKey, []byte(m.ID), nil)
	}
	return nil
}

func (s *store) Write(ctx context.Context, e *entities.EmailEvent) error {
	return s.enqueue(ctx, writeOp{event: e})
}

func (s *store) WriteMessage(ctx context.Context, m *entities.EmailMessage) error {
	return s.enqueue(ctx, messageOp{message: m})
}

func (s *store) GetByID(ctx context.Context, tenantID string, id string) (*entities.EmailEvent, error) {
	idKey := []byte(fmt.Sprintf("event_id:%s", id))
	idVal, closer, err := s.db.Get(idKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()

	parts := strings.Split(string(idVal), ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid index value")
	}
	actualTenantID := parts[0]
	if actualTenantID != tenantID {
		return nil, nil // Not authorized
	}
	timestampDesc := parts[1]

	key := []byte(fmt.Sprintf("events:%s:%s:%s", tenantID, timestampDesc, id))
	val, closer2, err := s.db.Get(key)
	if err != nil {
		return nil, err
	}
	defer closer2.Close()

	var e entities.EmailEvent
	if err := json.Unmarshal(val, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *store) ListByMessageID(ctx context.Context, tenantID string, messageID string) ([]*entities.EmailEvent, error) {
	prefix := []byte(fmt.Sprintf("msg_events:%s:%s:", tenantID, messageID))
	iter, _ := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: append(slices.Clone(prefix), 0xFF),
	})
	defer iter.Close()

	var events []*entities.EmailEvent
	for iter.First(); iter.Valid() && strings.HasPrefix(string(iter.Key()), string(prefix)); iter.Next() {
		var e entities.EmailEvent
		if err := json.Unmarshal(iter.Value(), &e); err == nil {
			events = append(events, &e)
		}
	}
	return events, nil
}

func (s *store) GetMessage(ctx context.Context, tenantID string, messageID string) (*entities.EmailMessage, error) {
	key := []byte(fmt.Sprintf("messages:%s:%s", tenantID, messageID))
	val, closer, err := s.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()

	var m entities.EmailMessage
	if err := json.Unmarshal(val, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *store) GetLatestMessageForRecipient(ctx context.Context, tenantID string, recipient string) (*entities.EmailMessage, error) {
	prefix := []byte(fmt.Sprintf("recipient_messages:%s:%s:", tenantID, recipient))
	iter, _ := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: append(slices.Clone(prefix), 0xFF),
	})
	defer iter.Close()

	if iter.First() {
		// Since it's sorted by timestamp_desc, the first one is the latest
		messageID := string(iter.Value())
		return s.GetMessage(ctx, tenantID, messageID)
	}
	return nil, nil
}

func (s *store) WriteResourceMetric(ctx context.Context, cpuUsage float64, memUsage uint64, load15 float64) error {
	op := resourceOp{
		point: entities.ResourcePoint{
			Timestamp:    time.Now(),
			CPUUsage:     cpuUsage,
			MemoryUsage:  memUsage,
			SystemLoad15: load15,
		},
	}
	return s.enqueue(ctx, op)
}

func (s *store) performWriteResource(batch *pebble.Batch, p entities.ResourcePoint) error {
	timestampDesc := math.MaxInt64 - p.Timestamp.UnixNano()
	key := []byte(fmt.Sprintf("resource:%019d", timestampDesc))
	val, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return batch.Set(key, val, nil)
}

func (s *store) GetResourceHistory(ctx context.Context, since time.Time) ([]entities.ResourcePoint, error) {
	prefix := []byte("resource:")
	lower := prefix
	upper := []byte("resource;")

	if !since.IsZero() {
		tsDesc := math.MaxInt64 - since.UnixNano()
		// resource:desc_timestamp
		// Range is [resource:0, resource:desc_timestamp_of_since]
		upper = []byte(fmt.Sprintf("resource:%019d", tsDesc))
	}

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: lower,
		UpperBound: upper,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var res []entities.ResourcePoint
	for iter.SeekGE(lower); iter.Valid(); iter.Next() {
		var p entities.ResourcePoint
		if err := json.Unmarshal(iter.Value(), &p); err == nil {
			res = append(res, p)
		}
	}
	return res, nil
}

func (s *store) ListArchives(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]entities.ArchiveInfo, string, error) {
	if tenantID == "" {
		return nil, "", errors.New("tenant id is mandatory")
	}

	entries, err := os.ReadDir(tenantArchiveDir(tenantID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}

	var all []entities.ArchiveInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		all = append(all, entities.ArchiveInfo{
			ID:        entry.Name(),
			Filename:  entry.Name(),
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}

	// Sort by date descending
	slices.SortFunc(all, func(a, b entities.ArchiveInfo) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})

	startIndex := 0
	if pageToken != "" {
		for i, a := range all {
			if a.ID == pageToken {
				startIndex = i
				break
			}
		}
	}

	endIndex := startIndex + pageSize
	if endIndex > len(all) {
		endIndex = len(all)
	}

	var res []entities.ArchiveInfo
	if startIndex < len(all) {
		res = all[startIndex:endIndex]
	}

	nextPageToken := ""
	if endIndex < len(all) {
		nextPageToken = all[endIndex].ID
	}

	return res, nextPageToken, nil
}

func (s *store) GetArchive(ctx context.Context, tenantID, id string) ([]byte, string, error) {
	if tenantID == "" {
		return nil, "", errors.New("tenant id is mandatory")
	}

	// Base strips any directory the caller supplied, so an archive can only be
	// read from the requesting tenant's own directory.
	id = filepath.Base(id)
	archivePath := filepath.Join(tenantArchiveDir(tenantID), id)

	content, err := os.ReadFile(archivePath)
	if err != nil {
		return nil, "", err
	}

	return content, id, nil
}

func (s *store) incrementCounter(batch *pebble.Batch, key []byte) error {
	one := make([]byte, 8)
	binary.LittleEndian.PutUint64(one, 1)
	return batch.Merge(key, one, nil)
}

func (s *store) List(ctx context.Context, tenantID string, filter stores.ListFilter) ([]*entities.EmailEvent, string, error) {
	if filter.PageSize <= 0 {
		filter.PageSize = 50
	}
	var lowerBound []byte
	var upperBound []byte

	if filter.MessageID != "" {
		var prefix []byte
		if filter.Recipient != "" && filter.RecipientExact {
			prefix = []byte(fmt.Sprintf("msg_events:%s:%s:%s:", tenantID, filter.MessageID, filter.Recipient))
		} else {
			prefix = []byte(fmt.Sprintf("msg_events:%s:%s:", tenantID, filter.MessageID))
		}
		lowerBound = prefix
		upperBound = append(slices.Clone(prefix), 0xFF)
	} else if filter.LatestOnly {
		// Use latest_events index: latest_events:{tenant_id}:{timestamp_desc}:{message_id}:{recipient}
		prefix := []byte(fmt.Sprintf("latest_events:%s:", tenantID))
		lowerBound = prefix
		upperBound = append(slices.Clone(prefix), 0xFF)

		if !filter.EndTime.IsZero() {
			timestampDesc := math.MaxInt64 - filter.EndTime.UnixNano()
			lowerBound = []byte(fmt.Sprintf("latest_events:%s:%019d:", tenantID, timestampDesc))
		}

		if !filter.StartTime.IsZero() {
			timestampDesc := math.MaxInt64 - filter.StartTime.UnixNano()
			upperBound = []byte(fmt.Sprintf("latest_events:%s:%019d:", tenantID, timestampDesc+1))
		}
	} else {
		prefix := []byte(fmt.Sprintf("events:%s:", tenantID))
		lowerBound = prefix
		upperBound = append(slices.Clone(prefix), 0xFF)

		if !filter.EndTime.IsZero() {
			// end_time is more recent -> smaller timestampDesc
			timestampDesc := math.MaxInt64 - filter.EndTime.UnixNano()
			lowerBound = []byte(fmt.Sprintf("events:%s:%019d:", tenantID, timestampDesc))
		}

		if !filter.StartTime.IsZero() {
			// start_time is older -> larger timestampDesc
			timestampDesc := math.MaxInt64 - filter.StartTime.UnixNano()
			upperBound = []byte(fmt.Sprintf("events:%s:%019d:", tenantID, timestampDesc+1))
		}
	}

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: lowerBound,
		UpperBound: upperBound,
	})
	if err != nil {
		return nil, "", err
	}
	defer iter.Close()

	if filter.PageToken != "" {
		iter.SeekGE([]byte(filter.PageToken))
	} else {
		iter.SeekGE(lowerBound)
	}

	var res []*entities.EmailEvent
	count := 0
	for ; iter.Valid() && count < filter.PageSize; iter.Next() {
		var e entities.EmailEvent
		if err := json.Unmarshal(iter.Value(), &e); err != nil {
			continue
		}

		// Filter by recipient
		if filter.Recipient != "" {
			if filter.RecipientExact {
				if e.Recipient != filter.Recipient {
					continue
				}
			} else if !strings.Contains(strings.ToLower(e.Recipient), strings.ToLower(filter.Recipient)) {
				continue
			}
		}

		// Filter by event type
		if filter.EventType != 0 && e.Type != filter.EventType {
			continue
		}

		// Filter by subject
		if filter.Subject != "" {
			if !strings.Contains(strings.ToLower(e.Subject), strings.ToLower(filter.Subject)) {
				continue
			}
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

func (s *store) GetMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time) (map[string]int64, error) {
	if startTime.IsZero() && endTime.IsZero() {
		// Return all-time total metrics from aggregated keys
		prefix := []byte(fmt.Sprintf("metrics:%s:", tenantID))
		upper := append(slices.Clone(prefix), 0xFF)

		iter, err := s.db.NewIter(&pebble.IterOptions{
			LowerBound: prefix,
			UpperBound: upper,
		})
		if err != nil {
			return nil, err
		}
		defer iter.Close()

		res := make(map[string]int64)
		for iter.SeekGE(prefix); iter.Valid(); iter.Next() {
			key := string(iter.Key())
			parts := strings.Split(key, ":")
			if len(parts) < 3 {
				continue
			}
			eventType := parts[len(parts)-1]
			val := decodeUint64(iter.Value())
			res[eventType] = int64(val)
		}
		return res, nil
	}

	// Calculate metrics using aggregated minute timeseries for better performance and persistence
	ts, err := s.GetTimeSeriesMetrics(ctx, tenantID, startTime, endTime, "minute")
	if err == nil && len(ts) > 0 {
		res := make(map[string]int64)
		for _, minMetrics := range ts {
			for eventType, count := range minMetrics {
				res[eventType] += count
			}
		}
		return res, nil
	}

	// FALLBACK: Calculate metrics by iterating over events in the time range
	prefix := []byte(fmt.Sprintf("events:%s:", tenantID))
	lower := prefix
	upper := append(slices.Clone(prefix), 0xFF)

	if !endTime.IsZero() {
		tsDesc := math.MaxInt64 - endTime.UnixNano()
		lower = []byte(fmt.Sprintf("events:%s:%019d:", tenantID, tsDesc))
	}

	if !startTime.IsZero() {
		tsDesc := math.MaxInt64 - startTime.UnixNano()
		upper = []byte(fmt.Sprintf("events:%s:%019d:", tenantID, tsDesc+1))
	}

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: lower,
		UpperBound: upper,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	res := make(map[string]int64)
	for iter.SeekGE(lower); iter.Valid(); iter.Next() {
		var e entities.EmailEvent
		if err := json.Unmarshal(iter.Value(), &e); err != nil {
			continue
		}
		typeStr := strings.TrimPrefix(e.Type.String(), "EMAIL_EVENT_TYPE_")
		res[typeStr]++
	}

	return res, nil
}

func (s *store) GetTimeSeriesMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time, granularity string) (map[string]map[string]int64, error) {
	if granularity == "" {
		granularity = "day"
	}

	// Use aggregated metrics where possible
	if granularity == "day" || granularity == "hour" || granularity == "minute" {
		prefixStr := "timeseries:"
		if granularity == "hour" {
			prefixStr = "timeseries:hour:"
		} else if granularity == "minute" {
			prefixStr = "timeseries:minute:"
		}
		prefix := []byte(fmt.Sprintf("%s%s:", prefixStr, tenantID))

		lowerBound := prefix
		upperBound := append(slices.Clone(prefix), 0xFF)

		if !startTime.IsZero() {
			format := "2006-01-02"
			if granularity == "hour" {
				format = "2006-01-02 15"
			} else if granularity == "minute" {
				format = "2006-01-02 15:04"
			}
			lowerBound = []byte(fmt.Sprintf("%s%s:%s", prefixStr, tenantID, startTime.Format(format)))
		}

		if !endTime.IsZero() {
			format := "2006-01-02"
			if granularity == "hour" {
				format = "2006-01-02 15"
			} else if granularity == "minute" {
				format = "2006-01-02 15:04"
			}
			upperBound = []byte(fmt.Sprintf("%s%s:%s\xff", prefixStr, tenantID, endTime.Format(format)))
		}

		iter, err := s.db.NewIter(&pebble.IterOptions{
			LowerBound: lowerBound,
			UpperBound: upperBound,
		})
		if err != nil {
			return nil, err
		}
		defer iter.Close()

		res := make(map[string]map[string]int64)
		for iter.SeekGE(lowerBound); iter.Valid(); iter.Next() {
			key := string(iter.Key())
			parts := strings.Split(key, ":")

			var timeKey, eventType string
			if granularity == "day" && len(parts) >= 4 {
				timeKey = parts[2]
				eventType = parts[3]
			} else if granularity == "hour" && len(parts) >= 5 {
				timeKey = parts[3]
				eventType = parts[4]
			} else if granularity == "minute" && len(parts) >= 6 {
				timeKey = parts[3] + ":" + parts[4]
				eventType = parts[5]
			} else {
				continue
			}

			val := int64(decodeUint64(iter.Value()))
			if _, ok := res[timeKey]; !ok {
				res[timeKey] = make(map[string]int64)
			}
			res[timeKey][eventType] = val
		}

		// If we found data in aggregated timeseries, return it
		if len(res) > 0 {
			return res, nil
		}

		// FALLBACK: if no aggregated data found, try to scan events log
		// This happens for very recent data that might not be fully flushed
		// or for periods where aggregation was not yet enabled.
	}

	// For minute granularity (fallback) or other cases, we must iterate events
	prefix := []byte(fmt.Sprintf("events:%s:", tenantID))
	lower := prefix
	upper := append(slices.Clone(prefix), 0xFF)

	if !endTime.IsZero() {
		tsDesc := math.MaxInt64 - endTime.UnixNano()
		lower = []byte(fmt.Sprintf("events:%s:%019d:", tenantID, tsDesc))
	}

	if !startTime.IsZero() {
		tsDesc := math.MaxInt64 - startTime.UnixNano()
		upper = []byte(fmt.Sprintf("events:%s:%019d:", tenantID, tsDesc+1))
	}

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: lower,
		UpperBound: upper,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	res := make(map[string]map[string]int64)
	for iter.SeekGE(lower); iter.Valid(); iter.Next() {
		var e entities.EmailEvent
		if err := json.Unmarshal(iter.Value(), &e); err != nil {
			continue
		}

		var timeKey string
		switch granularity {
		case "minute":
			timeKey = e.Timestamp.Format("2006-01-02 15:04")
		case "hour":
			timeKey = e.Timestamp.Format("2006-01-02 15:00")
		case "day":
			timeKey = e.Timestamp.Format("2006-01-02")
		default:
			timeKey = e.Timestamp.Format("2006-01-02")
		}

		typeStr := strings.TrimPrefix(e.Type.String(), "EMAIL_EVENT_TYPE_")
		if _, ok := res[timeKey]; !ok {
			res[timeKey] = make(map[string]int64)
		}
		res[timeKey][typeStr]++
	}

	return res, nil
}

// Close stops accepting writes, commits everything already accepted, and
// closes the database. Safe to call more than once, since the shared store and
// the wrappers around this one can each reach it on the way down.
func (s *store) Close() error {
	s.closeOnce.Do(func() {
		// Taken for writing, so every sender already past its closed check
		// has finished its send before the worker is told to drain.
		s.closeMu.Lock()
		s.closed = true
		s.closeMu.Unlock()

		close(s.stopCh)
		s.wg.Wait()
		s.closeErr = s.db.Close()
	})
	return s.closeErr
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
//
// Published as a gauge because retention is the thing operators forget and
// disk is where forgetting it lands. Most classes default to keeping data
// forever — deliberately, since a version bump must not start deleting what
// nobody agreed to lose — so growth here is expected rather than a fault, and
// the only honest protection is making it visible before the volume runs out.
//
// Pebble's own figure rather than a directory walk: it counts the WAL and
// files mid-compaction, which a walk either misses or double-counts, and it is
// a field read rather than a stat of every SSTable.
func (s *store) DiskUsage() int64 {
	if s.db == nil {
		return 0
	}
	return int64(s.db.Metrics().DiskSpaceUsage())
}
