package pebble

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

// Retention and the writer both maintain the latest-event pointer, and they
// used to do it from different goroutines. Retention read latest_ts, the writer
// moved it to a newer event, and retention's commit then deleted latest_ts
// anyway — stranding the newer latest_events entry with nothing pointing at it.
// The next event for that message and recipient added a second entry, and the
// timeline listed the message twice from then on.
//
// The invariant checked here is the one the timeline depends on: one
// latest_events entry per message and recipient, and latest_ts naming it.
func TestRetentionAndTheWriterAgreeOnTheLatestPointer(t *testing.T) {
	inArchiveDir(t)
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	st := s.(*store)

	const pairs = 3000
	old := time.Now().Add(-48 * time.Hour)
	write := func(id string, pair int, at time.Time) {
		if err := s.Write(t.Context(), &entities.EmailEvent{
			ID:        id,
			TenantID:  retentionTenant,
			MessageID: fmt.Sprintf("m%04d", pair),
			Recipient: "to@example.com",
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
			Timestamp: at,
		}); err != nil {
			t.Errorf("write %s: %v", id, err)
		}
	}

	for i := range pairs {
		write(fmt.Sprintf("old%04d", i), i, old)
	}
	waitLong(t, s, fmt.Sprintf("old%04d", pairs-1))

	// The newer events arrive while the pass is running, which is the window.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range pairs {
			write(fmt.Sprintf("new%04d", i), i, time.Now())
		}
	}()
	if _, err := s.TruncateBefore(t.Context(), time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}
	wg.Wait()
	waitLong(t, s, fmt.Sprintf("new%04d", pairs-1))
	// And one more for every pair after both have settled: with a stranded
	// entry this is the write that adds the duplicate.
	for i := range pairs {
		write(fmt.Sprintf("last%04d", i), i, time.Now())
	}
	waitLong(t, s, fmt.Sprintf("last%04d", pairs-1))
	time.Sleep(200 * time.Millisecond)

	entries := scanKeys(t, st.db, "latest_events:")
	perPair := make(map[string]int)
	for _, key := range entries {
		// latest_events:{tenant}:{tsDesc}:{message}:{recipient}
		parts := strings.SplitN(key, ":", 5)
		perPair[parts[3]+":"+parts[4]]++
	}
	for pair, n := range perPair {
		if n != 1 {
			t.Errorf("%s has %d latest_events entries; want 1", pair, n)
		}
	}
	if len(perPair) != pairs {
		t.Errorf("%d pairs have a latest entry; want %d", len(perPair), pairs)
	}
	if pointers := scanKeys(t, st.db, "latest_ts:"); len(pointers) != pairs {
		t.Errorf("%d latest_ts pointers; want %d", len(pointers), pairs)
	}
}

// waitLong is waitForEvent with room for thousands of writes under -race.
func waitLong(t *testing.T, s stores.EventRepository, id string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := s.GetByID(t.Context(), retentionTenant, id); got != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event %s never became readable", id)
}

func scanKeys(t *testing.T, db *pebble.DB, prefix string) []string {
	t.Helper()
	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: append([]byte(prefix), 0xFF),
	})
	if err != nil {
		t.Fatalf("iter: %v", err)
	}
	defer iter.Close()

	var keys []string
	for iter.First(); iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	return keys
}
