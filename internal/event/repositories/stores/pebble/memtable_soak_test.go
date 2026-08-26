//go:build soak

package pebble

import (
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/pkg/pebbleopt"
)

/*
What the memtable size actually costs, in both directions.

A store's footprint is dominated by write-ahead log, not by data, and the
memtable decides how much is unflushed at any moment. No retention setting
moves that floor. The obvious response is "make it smaller", and the obvious
objection is "that will hurt writes" -- neither had a number attached, so the
sizing decision kept getting deferred.

	go test -tags soak -run TestMemTableTradeoff -timeout 30m -v \
	  ./internal/event/repositories/stores/pebble/

Reports, per size: write throughput, the settled split between sstables and
WAL, and the total an operator has to buy disk for.
*/
func TestMemTableTradeoff(t *testing.T) {
	messageCount := soakInt(t, "SOAK_MESSAGES", 40_000)
	bodyBytes := soakInt(t, "SOAK_BODY_BYTES", 8192)

	body := make([]byte, bodyBytes)
	for i := range body {
		body[i] = 'a' + byte(i%26)
	}

	sizes := []int{64, 32, 16, 8, 4}
	t.Logf("%d messages of %d bytes at each size", messageCount, bodyBytes)
	t.Logf("%-6s %10s %12s %10s %10s", "memMB", "writes/s", "total", "sstables", "wal")

	for _, mb := range sizes {
		t.Run(fmt.Sprintf("%dMB", mb), func(t *testing.T) {
			t.Setenv(pebbleopt.EnvMemTableMB, fmt.Sprint(mb))
			if got := pebbleopt.MemTableMB(); got != mb {
				t.Fatalf("memtable size = %d; want %d", got, mb)
			}

			dir := t.TempDir()
			store, err := NewStore(dir)
			if err != nil {
				t.Fatalf("failed to create store: %v", err)
			}

			start := time.Now()
			for i := range messageCount {
				err := store.WriteMessage(t.Context(), &entities.EmailMessage{
					ID:        fmt.Sprintf("msg-%d", i),
					TenantID:  "tenant-a",
					To:        []string{fmt.Sprintf("user%d@example.org", i%5000)},
					Subject:   fmt.Sprintf("subject %d", i),
					BodyHTML:  string(body),
					CreatedAt: time.Now(),
				})
				if err != nil {
					t.Fatalf("write %d: %v", i, err)
				}
			}
			// A generous budget rather than the shared one-second helper: at the
			// small sizes the pipeline is genuinely slower to drain, which is the
			// thing being measured. Timing out here would report the slow
			// configuration as broken instead of as slow.
			deadline := time.Now().Add(2 * time.Minute)
			for time.Now().Before(deadline) {
				if got, _ := store.GetMessage(t.Context(), "tenant-a",
					fmt.Sprintf("msg-%d", messageCount-1)); got != nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if got, _ := store.GetMessage(t.Context(), "tenant-a",
				fmt.Sprintf("msg-%d", messageCount-1)); got == nil {
				t.Fatalf("the store never drained at %d MiB", mb)
			}
			elapsed := time.Since(start)

			// Measured after a close and reopen, which retires the WAL the way a
			// restart does. Anything still there is the floor.
			if err := store.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			reopened, err := NewStore(dir)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer reopened.Close()

			settled := measure(t, dir)
			rate := float64(messageCount) / elapsed.Seconds()
			t.Logf("%-6d %10.0f %12s %10s %10s",
				mb, rate, mib(settled.total), mib(settled.sstables), mib(settled.wal))
		})
	}

}
