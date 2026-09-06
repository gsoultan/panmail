// Package postgres writes delivery events to the shared database.
//
// It exists because events.db is local to a gateway: with three replicas the
// dashboard shows whatever the instance serving the request happened to see.
// docs/design/0002-shared-event-store.md has the measurement.
//
// This is step two of the sequencing in that document. Nothing reads these rows
// yet — they are written alongside Pebble so the two can be compared under the
// same traffic before any read moves.
package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/pkg/db"
)

//go:embed sql/insert_events.sql
var insertEventsQuery string

const (
	// batchSize matches the Pebble writer's. The measurement says 2,000 is
	// three times faster, and 500 already has 4.3x the headroom the send path
	// can use — so the smaller batch is chosen for what it costs on failure
	// rather than for speed: it loses less when a flush fails and holds a
	// connection for less time.
	batchSize = 500

	// flushInterval bounds how long an event waits when the rate is low. Also
	// the Pebble writer's.
	flushInterval = 100 * time.Millisecond

	// queueDepth is the buffer in front of the writer. Deliberately shallower
	// than Pebble's 20,000: this is a shadow writer, and a deep buffer would
	// let it absorb a long database outage and then flood the pool on
	// recovery. When it fills, events are dropped and counted, because
	// blocking the send path to record what it did would be the wrong trade.
	queueDepth = 8192
)

// columnsPerRow is the width of one VALUES tuple in insert_events.sql.
const columnsPerRow = 11

// Writer batches events onto the shared database.
//
// The shape is deliberately the same as the Pebble store's: a channel, a
// background worker, a batch of 500 or 100 ms. The send path's cost for an
// event stays one channel send, which is the property the whole design is
// built to keep.
type Writer struct {
	conn db.Connection
	ch   chan *entities.EmailEvent

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	// Counters, published as metrics. written and dropped are what the
	// comparison in step two is made of: Pebble's count against written, with
	// dropped explaining any gap that is not a bug.
	written atomic.Int64
	dropped atomic.Int64
	failed  atomic.Int64
}

func NewWriter(conn db.Connection) *Writer {
	return &Writer{
		conn: conn,
		ch:   make(chan *entities.EmailEvent, queueDepth),
		stop: make(chan struct{}),
	}
}

// Write queues an event. It never blocks and never returns an error.
//
// Both are deliberate. This runs on the send path, behind the same call that
// writes to Pebble, and a shadow store that could slow a send down or fail one
// would be worse than the divergence it exists to remove. A full queue drops
// and counts.
func (w *Writer) Write(e *entities.EmailEvent) {
	if e == nil {
		return
	}
	select {
	case w.ch <- e:
	default:
		w.dropped.Add(1)
	}
}

// Start runs the batching loop until ctx ends.
func (w *Writer) Start(ctx context.Context) {
	w.wg.Add(1)
	defer w.wg.Done()

	batch := make([]*entities.EmailEvent, 0, batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// context.WithoutCancel: a flush that started before shutdown should
		// finish, for the same reason the outbox drains rather than abandoning
		// a batch mid-send.
		w.flush(context.WithoutCancel(ctx), batch)
		batch = batch[:0]
	}

	for {
		select {
		case e := <-w.ch:
			batch = append(batch, e)
			if len(batch) >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			// Drain what is already queued rather than discarding it. Bounded
			// by the buffer, so this cannot delay shutdown indefinitely.
			for {
				select {
				case e := <-w.ch:
					batch = append(batch, e)
					if len(batch) >= batchSize {
						flush()
					}
					continue
				default:
				}
				break
			}
			flush()
			return
		case <-w.stop:
			flush()
			return
		}
	}
}

// Stop ends the loop and waits for the final flush.
func (w *Writer) Stop() {
	w.stopOnce.Do(func() { close(w.stop) })
	w.wg.Wait()
}

// Stats reports what the writer has done, for the metrics that make step two's
// comparison possible.
func (w *Writer) Stats() (written, dropped, failed int64) {
	return w.written.Load(), w.dropped.Load(), w.failed.Load()
}

func (w *Writer) flush(ctx context.Context, batch []*entities.EmailEvent) {
	if !w.conn.IsConnected() {
		w.failed.Add(int64(len(batch)))
		return
	}

	statement, args := renderInsert(batch)
	if _, err := w.conn.GetDB().ExecContext(ctx, statement, args...); err != nil {
		w.failed.Add(int64(len(batch)))
		// Warn, not error: Pebble still has these events and every read still
		// comes from it, so this is a shadow falling behind rather than data
		// being lost.
		slog.Warn("could not write delivery events to the shared store",
			"error", err, "events", len(batch))
		return
	}
	w.written.Add(int64(len(batch)))
	w.bumpCounters(ctx, batch)
}

// renderInsert builds the multi-row VALUES list and its arguments.
func renderInsert(batch []*entities.EmailEvent) (string, []any) {
	var rows strings.Builder
	args := make([]any, 0, len(batch)*columnsPerRow)

	for i, e := range batch {
		if i > 0 {
			rows.WriteByte(',')
		}
		rows.WriteByte('(')
		for c := range columnsPerRow {
			if c > 0 {
				rows.WriteByte(',')
			}
			rows.WriteByte('$')
			rows.WriteString(strconv.Itoa(i*columnsPerRow + c + 1))
		}
		rows.WriteByte(')')

		args = append(args,
			e.ID, e.TenantID,
			nullableUUID(e.ProviderID), nullableText(e.ProviderName),
			e.MessageID, e.Type.String(),
			e.Recipient, e.Subject,
			e.Timestamp.UTC(), encodeMetadata(e.Metadata),
			nullableText(e.ErrorMessage))
	}

	return strings.Replace(insertEventsQuery, "__ROWS__", rows.String(), 1), args
}

// nullableUUID writes SQL NULL for an absent provider rather than the empty
// string, which a UUID column on PostgreSQL rejects outright. An event
// recorded before a provider is chosen genuinely has none.
func nullableUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// encodeMetadata stores the map as JSON text, or NULL when there is none.
//
// A map that will not marshal becomes NULL rather than failing the batch: the
// other 499 events in it are not at fault, and metadata is the least valuable
// field on the row.
func encodeMetadata(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return string(encoded)
}

// bumpCounters keeps the lifetime totals the dashboard's headline figures read.
//
// Grouped first, so a batch of 500 events costs one upsert per (tenant, type)
// rather than 500 — typically three, since a message produces PENDING, SENT and
// DELIVERED.
//
// A failure here is logged and nothing else. The events themselves are already
// committed, and a counter that is briefly behind is a worse outcome than
// failing the flush that would also lose them.
func (w *Writer) bumpCounters(ctx context.Context, batch []*entities.EmailEvent) {
	type key struct{ tenant, eventType string }
	counts := make(map[key]int64, 4)
	for _, e := range batch {
		counts[key{e.TenantID, e.Type.String()}]++
	}

	for k, n := range counts {
		if _, err := w.conn.GetDB().ExecContext(ctx,
			`INSERT INTO email_event_counters (tenant_id, type, count)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (tenant_id, type)
			 DO UPDATE SET count = email_event_counters.count + EXCLUDED.count`,
			k.tenant, k.eventType, n); err != nil {
			slog.Warn("could not update the event counters",
				"error", err, "tenant_id", k.tenant, "type", k.eventType)
			return
		}
	}
}
