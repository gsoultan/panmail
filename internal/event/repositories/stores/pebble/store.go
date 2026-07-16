package pebble

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

type store struct {
	db      *pebble.DB
	bufPool sync.Pool
	opChan  chan any
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

type writeOp struct {
	event *entities.EmailEvent
}

type messageOp struct {
	message *entities.EmailMessage
}

type resourceOp struct {
	point entities.ResourcePoint
}

func NewStore(dir string) (stores.EventRepository, error) {
	opts := &pebble.Options{
		Merger: &pebble.Merger{
			Name: "pebble.concatenate", // Use default name for compatibility with existing DBs
			Merge: func(key, value []byte) (pebble.ValueMerger, error) {
				return &counterValueMerger{sum: decodeUint64(value)}, nil
			},
		},
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

	count := 0
	for {
		select {
		case op := <-s.opChan:
			switch v := op.(type) {
			case writeOp:
				_ = s.performWrite(batch, v.event)
			case messageOp:
				_ = s.performWriteMessage(batch, v.message)
			case resourceOp:
				_ = s.performWriteResource(batch, v.point)
			}
			count++
			if count >= 500 {
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

func (s *store) performWrite(batch *pebble.Batch, e *entities.EmailEvent) error {
	timestampDesc := math.MaxInt64 - e.Timestamp.UnixNano()
	tsDescStr := strconv.FormatInt(timestampDesc, 10)
	// Ensure 19 digits for sorting
	if len(tsDescStr) < 19 {
		tsDescStr = strings.Repeat("0", 19-len(tsDescStr)) + tsDescStr
	}

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

	// msg_events index: msg_events:{tenant_id}:{message_id}:{timestamp_desc}:{event_id}
	if e.MessageID != "" {
		buf = buf[:0]
		buf = append(buf, "msg_events:"...)
		buf = append(buf, e.TenantID...)
		buf = append(buf, ':')
		buf = append(buf, e.MessageID...)
		buf = append(buf, ':')
		buf = append(buf, tsDescStr...)
		buf = append(buf, ':')
		buf = append(buf, e.ID...)
		msgEventKey := make([]byte, len(buf))
		copy(msgEventKey, buf)
		_ = batch.Set(msgEventKey, val, nil)
	}

	return nil
}

func (s *store) performWriteMessage(batch *pebble.Batch, m *entities.EmailMessage) error {
	key := []byte(fmt.Sprintf("messages:%s:%s", m.TenantID, m.ID))
	val, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return batch.Set(key, val, nil)
}

func (s *store) Write(ctx context.Context, e *entities.EmailEvent) error {
	select {
	case s.opChan <- writeOp{event: e}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *store) WriteMessage(ctx context.Context, m *entities.EmailMessage) error {
	select {
	case s.opChan <- messageOp{message: m}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

func (s *store) TruncateBefore(ctx context.Context, before time.Time) error {
	timestampDesc := math.MaxInt64 - before.UnixNano()

	prefix := []byte("events:")
	upper := []byte("events;") // ';' is next char after ':'

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	// Prepare archive file
	archiveDir := "archives"
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("failed to create archives directory: %w", err)
	}

	filename := fmt.Sprintf("archive_%s.jsonl", time.Now().Format("20060102_150405"))
	archivePath := filepath.Join(archiveDir, filename)
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}
	defer archiveFile.Close()

	batch := s.db.NewBatch()
	count := 0
	archivedCount := 0
	for iter.SeekGE(prefix); iter.Valid(); iter.Next() {
		key := iter.Key()
		parts := strings.Split(string(key), ":")
		if len(parts) < 4 {
			continue
		}

		// Key: events:{tenant_id}:{timestamp_desc}:{id}
		ts, _ := strconv.ParseInt(parts[2], 10, 64)
		if ts > timestampDesc {
			var e entities.EmailEvent
			if err := json.Unmarshal(iter.Value(), &e); err == nil {
				// Write to archive
				data, _ := json.Marshal(e)
				_, _ = archiveFile.Write(append(data, '\n'))
				archivedCount++

				// Cleanup msg_events index
				if e.MessageID != "" {
					_ = batch.Delete([]byte(fmt.Sprintf("msg_events:%s:%s:%s:%s", parts[1], e.MessageID, parts[2], parts[3])), nil)
				}
			}

			id := parts[3]
			_ = batch.Delete(key, nil)
			_ = batch.Delete([]byte(fmt.Sprintf("event_id:%s", id)), nil)
			count++
		}

		// Commit every 1000 deletions to keep memory low
		if count >= 1000 {
			if err := batch.Commit(pebble.Sync); err != nil {
				return err
			}
			batch = s.db.NewBatch()
			count = 0
		}
	}

	if count > 0 {
		if err := batch.Commit(pebble.Sync); err != nil {
			return err
		}
	} else {
		batch.Close()
	}

	// If nothing was archived, delete the empty file
	if archivedCount == 0 {
		archiveFile.Close()
		_ = os.Remove(archivePath)
	}

	return nil
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
	select {
	case s.opChan <- op:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

func (s *store) ListArchives(ctx context.Context, pageSize int, pageToken string) ([]entities.ArchiveInfo, string, error) {
	archiveDir := "archives"
	entries, err := os.ReadDir(archiveDir)
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

func (s *store) GetArchive(ctx context.Context, id string) ([]byte, string, error) {
	// Simple validation to prevent path traversal
	id = filepath.Base(id)
	archivePath := filepath.Join("archives", id)

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
	var lowerBound []byte
	var upperBound []byte

	if filter.MessageID != "" {
		// Use msg_events index: msg_events:{tenant_id}:{message_id}:{timestamp_desc}:{event_id}
		prefix := []byte(fmt.Sprintf("msg_events:%s:%s:", tenantID, filter.MessageID))
		lowerBound = prefix
		upperBound = make([]byte, len(prefix))
		copy(upperBound, prefix)
		upperBound[len(upperBound)-1]++
	} else {
		prefix := []byte(fmt.Sprintf("events:%s:", tenantID))
		lowerBound = prefix
		upperBound = make([]byte, len(prefix))
		copy(upperBound, prefix)
		upperBound[len(upperBound)-1]++

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
		if iter.Valid() && string(iter.Key()) == filter.PageToken {
			iter.Next()
		}
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
		if filter.Recipient != "" && !strings.Contains(strings.ToLower(e.Recipient), strings.ToLower(filter.Recipient)) {
			continue
		}

		// Filter by event type
		if filter.EventType != 0 && e.Type != filter.EventType {
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

func (s *store) GetMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time) (map[string]int64, error) {
	if startTime.IsZero() && endTime.IsZero() {
		// Return all-time total metrics from aggregated keys
		prefix := []byte(fmt.Sprintf("metrics:%s:", tenantID))
		upper := make([]byte, len(prefix))
		copy(upper, prefix)
		upper[len(upper)-1]++

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
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	upper[len(upper)-1]++

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
		upperBound := make([]byte, len(prefix))
		copy(upperBound, prefix)
		upperBound[len(upperBound)-1]++

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
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	upper[len(upper)-1]++

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

func (s *store) Close() error {
	close(s.stopCh)
	s.wg.Wait()
	return s.db.Close()
}
