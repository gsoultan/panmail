// Package pebbleopt holds the Pebble tuning shared by panmail's three
// key-value stores.
package pebbleopt

import (
	"log/slog"
	"os"
	"strconv"
)

// DefaultMemTableMB is what the stores have always used.
const DefaultMemTableMB = 64

// EnvMemTableMB overrides it.
const EnvMemTableMB = "PANMAIL_PEBBLE_MEMTABLE_MB"

// memtable bounds. Below 1 MiB Pebble flushes so often that the write path
// stalls behind compaction; above 512 MiB a single store can hold more
// unflushed data than most gateways have RAM to spare.
const (
	minMemTableMB = 1
	maxMemTableMB = 512
)

// MemTableSize is the memtable size the stores open with, in bytes.
//
// This is the lever for disk footprint, and it is worth understanding before
// touching it. A Pebble store's size on disk is dominated by write-ahead log,
// not by data: everything written since the last flush lives in a WAL, the
// memtable decides how much that is, and Pebble recycles those files rather
// than deleting them. No retention setting moves that floor, because retention
// can only reclaim what has already been written into sstables.
//
// So a smaller memtable means a smaller resting footprint and more frequent
// flushes; a larger one means fewer flushes and a bigger floor. Measured
// numbers are in docs/scaling.md — read them rather than guessing, because the
// throughput cost is not linear and the disk saving is larger than it looks.
//
// Left alone, this returns exactly what panmail has always used.
func MemTableSize() uint64 {
	return uint64(MemTableMB()) << 20
}

// MemTableMB is the configured size in mebibytes, clamped into a range that
// still works.
func MemTableMB() int {
	raw := os.Getenv(EnvMemTableMB)
	if raw == "" {
		return DefaultMemTableMB
	}

	mb, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("ignoring an unreadable pebble memtable size",
			"env", EnvMemTableMB, "value", raw, "using", DefaultMemTableMB)
		return DefaultMemTableMB
	}

	switch {
	case mb < minMemTableMB:
		slog.Warn("pebble memtable size raised to the minimum that still writes",
			"asked", mb, "using", minMemTableMB)
		return minMemTableMB
	case mb > maxMemTableMB:
		slog.Warn("pebble memtable size capped",
			"asked", mb, "using", maxMemTableMB)
		return maxMemTableMB
	}
	return mb
}
