//go:build soak

package pebble

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

/*
Retention against a store the size a real deployment reaches.

The unit tests prove the cutoffs are right on a handful of rows. They say
nothing about what only appears at size: a pass that scans every key and then
rewrites the sstables it deleted from. Two failures live there and nowhere
else — a scan whose memory grows with the store, and a prune that deletes rows
without ever returning the disk, which looks exactly like retention not working
to the operator who set it.

Excluded from the normal suite by a build tag, because it writes hundreds of
megabytes:

	go test -tags soak -run TestRetentionSoak -timeout 30m -v \
	  ./internal/event/repositories/stores/pebble/

Size is tunable for a bigger run:

	SOAK_EVENTS=1000000 SOAK_MESSAGES=250000 SOAK_BODY_BYTES=16384 go test ...

# Reading the numbers

A Pebble store is sstables plus write-ahead log, and only the first is
retention's business. Most of a busy store is WAL — the writer commits without
syncing and the memtable is 64 MiB, so a great deal of recent data lives there
rather than in a file any compaction can rewrite. Pebble then *recycles* those
files instead of deleting them, so a pass that reclaimed every byte it could
still leaves the directory much the same size until they are reused.

So the assertion is on sstable bytes, and the totals are reported. Asserting on
the total would be a test of Pebble's WAL policy wearing retention's name.
*/

func soakInt(t *testing.T, name string, fallback int) int {
	t.Helper()

	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s=%q is not a number: %v", name, raw, err)
	}
	return value
}

// storeSize reports the bytes on disk, split by what they are.
//
// A file that vanishes mid-walk is one Pebble compacted away underneath us,
// which is to say one that is no longer taking up space.
type storeSize struct {
	sstables int64
	wal      int64
	total    int64
}

func (s storeSize) String() string {
	return fmt.Sprintf("%s total (%s sstables, %s wal)", mib(s.total), mib(s.sstables), mib(s.wal))
}

func measure(t *testing.T, dir string) storeSize {
	t.Helper()

	var size storeSize
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // a compacted-away file takes no space
		}
		size.total += info.Size()
		switch filepath.Ext(path) {
		case ".sst":
			size.sstables += info.Size()
		case ".log":
			size.wal += info.Size()
		}
		return nil
	})
	return size
}

func mib(bytes int64) string { return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20)) }

func TestRetentionSoak(t *testing.T) {
	const tenant = "tenant-soak"

	eventCount := soakInt(t, "SOAK_EVENTS", 300_000)
	messageCount := soakInt(t, "SOAK_MESSAGES", 60_000)
	bodyBytes := soakInt(t, "SOAK_BODY_BYTES", 8192)

	// archiveRoot is relative to the working directory, which under `go test`
	// is the package directory. Without this the pass writes every expired
	// event into the source tree — 124 MiB of JSONL sitting in
	// internal/event/.../pebble/archives/, which is exactly what happened the
	// first time this ran.
	inArchiveDir(t)

	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// A year of history, so a cutoff can expire most of it and leave a recent
	// tail that has to survive intact.
	const spread = 365 * 24 * time.Hour
	now := time.Now()
	oldest := now.Add(-spread)

	body := make([]byte, bodyBytes)
	for i := range body {
		body[i] = 'a' + byte(i%26)
	}

	t.Logf("writing %d events and %d messages of %d bytes", eventCount, messageCount, bodyBytes)
	writeStart := time.Now()
	seedEvents(t, store, tenant, eventCount, oldest, spread)
	seedMessages(t, store, tenant, messageCount, oldest, spread, string(body))
	t.Logf("seeded in %s", time.Since(writeStart).Round(time.Millisecond))

	before := measure(t, dir)
	t.Logf("before: %s", before)

	// Below the memtable size nothing has been flushed, so the store is all
	// WAL and there are no sstables for a compaction to shrink. That is not a
	// pass — it is a run that cannot see the thing it exists to measure, and
	// reporting it as green would be worse than not running it.
	if before.sstables == 0 {
		t.Skipf("the store never left its 64 MiB memtable (%s, no sstables): "+
			"raise SOAK_EVENTS/SOAK_MESSAGES/SOAK_BODY_BYTES past it for compaction to be measurable",
			before)
	}

	// Expire the oldest 90%, which is the shape of a real first pass after an
	// operator finally sets a retention.
	cutoff := now.Add(-spread / 10)

	var memBefore, memAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	start := time.Now()
	removedEvents, err := store.TruncateBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}
	eventsElapsed := time.Since(start)

	start = time.Now()
	removedMessages, err := store.TruncateMessagesBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("TruncateMessagesBefore: %v", err)
	}
	messagesElapsed := time.Since(start)

	runtime.ReadMemStats(&memAfter)
	after := measure(t, dir)
	heapGrowth := int64(memAfter.HeapAlloc) - int64(memBefore.HeapAlloc)

	t.Logf("events:   removed %d in %s", removedEvents, eventsElapsed.Round(time.Millisecond))
	t.Logf("messages: removed %d in %s", removedMessages, messagesElapsed.Round(time.Millisecond))
	t.Logf("after:  %s", after)
	t.Logf("heap grew %s during the pass", mib(heapGrowth))

	if removedEvents == 0 || removedMessages == 0 {
		t.Fatalf("removed %d events and %d messages; the cutoff expired nothing",
			removedEvents, removedMessages)
	}

	// A scan that holds the store in memory is the other failure that only
	// shows at size. Batches commit every 1000 keys, so growth should be a
	// fraction of the store rather than proportional to it.
	if heapGrowth > before.total/4 {
		t.Errorf("heap grew %s pruning a %s store; the scan is not bounded",
			mib(heapGrowth), mib(before.total))
	}

	assertRecentSurvives(t, store, tenant, cutoff)

	// Measured after a restart, because that is when it has settled: the
	// tombstones written by the pass are themselves in a WAL until the next
	// flush, and background compactions are still running when the pass
	// returns.
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	settled := measure(t, dir)
	t.Logf("settled: %s", settled)

	// The assertion this whole test exists for. Without the flush-then-compact
	// in compact(), the rows go and the sstables stay as they were — retention
	// that frees nothing, which from outside is indistinguishable from
	// retention that never ran.
	if settled.sstables >= before.sstables {
		t.Errorf("sstables did not shrink: %s before, %s settled — compaction reclaimed nothing",
			mib(before.sstables), mib(settled.sstables))
	}
	if reclaimed := float64(before.sstables-settled.sstables) / float64(before.sstables); reclaimed < 0.5 {
		t.Errorf("reclaimed only %.0f%% of the sstables after expiring 90%% of the store", reclaimed*100)
	}

	// Reported, never asserted — and worth reading before promising anyone
	// that retention shrinks their disk.
	//
	// A store's footprint is dominated by write-ahead log, not by data: the
	// memtable is 64 MiB and the writer commits without syncing, so most
	// recent data lives in a WAL rather than in any file a compaction can
	// rewrite. Pebble then recycles those files instead of deleting them, so
	// they survive a restart. Retention returns the live data; the directory
	// keeps a floor the size of the WAL high-water mark, and no retention
	// setting will move it.
	t.Logf("wal floor: %s of the settled %s is write-ahead log, holding %s of live data — "+
		"retention cannot reclaim this and a restart does not either",
		mib(settled.wal), mib(settled.total), mib(settled.sstables))
}

func seedEvents(t *testing.T, store stores.EventRepository, tenant string, count int, oldest time.Time, spread time.Duration) {
	t.Helper()

	ctx := context.Background()
	step := spread / time.Duration(count)

	for i := range count {
		err := store.Write(ctx, &entities.EmailEvent{
			ID:        fmt.Sprintf("evt-%d", i),
			TenantID:  tenant,
			MessageID: fmt.Sprintf("msg-%d", i%1000),
			Recipient: fmt.Sprintf("user%d@example.org", i%5000),
			Subject:   "soak",
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
			Timestamp: oldest.Add(step * time.Duration(i)),
		})
		if err != nil {
			t.Fatalf("write event %d: %v", i, err)
		}
	}
	waitForReadable(t, func() bool {
		got, _ := store.GetByID(context.Background(), tenant, fmt.Sprintf("evt-%d", count-1))
		return got != nil
	}, "events")
}

func seedMessages(t *testing.T, store stores.EventRepository, tenant string, count int, oldest time.Time, spread time.Duration, body string) {
	t.Helper()

	ctx := context.Background()
	step := spread / time.Duration(count)

	for i := range count {
		err := store.WriteMessage(ctx, &entities.EmailMessage{
			ID:        fmt.Sprintf("msg-%d", i),
			TenantID:  tenant,
			To:        []string{fmt.Sprintf("user%d@example.org", i%5000)},
			Cc:        []string{"archive@example.org"},
			Subject:   fmt.Sprintf("soak message %d", i),
			BodyHTML:  body,
			BodyText:  "soak",
			CreatedAt: oldest.Add(step * time.Duration(i)),
		})
		if err != nil {
			t.Fatalf("write message %d: %v", i, err)
		}
	}
	waitForReadable(t, func() bool {
		got, _ := store.GetMessage(context.Background(), tenant, fmt.Sprintf("msg-%d", count-1))
		return got != nil
	}, "messages")
}

// waitForReadable waits for the store's asynchronous writer to catch up, so the
// pass measures a settled store rather than one still absorbing writes.
func waitForReadable(t *testing.T, ready func() bool, what string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never became readable", what)
}

// assertRecentSurvives checks the tail the cutoff was supposed to keep.
//
// A prune that took everything would satisfy every size assertion above — the
// store would shrink beautifully.
func assertRecentSurvives(t *testing.T, store stores.EventRepository, tenant string, cutoff time.Time) {
	t.Helper()

	events, _, err := store.List(context.Background(), tenant, stores.ListFilter{PageSize: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("every event was removed, including those inside the retention window")
	}
	for _, e := range events {
		if e.Timestamp.Before(cutoff) {
			t.Errorf("event %s at %s survived a cutoff of %s", e.ID, e.Timestamp, cutoff)
			break
		}
	}
}
