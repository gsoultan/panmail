// Package cache provides a small bounded cache for values that are cheap to
// recompute but expensive to fetch.
package cache

import (
	"sync"
	"time"
)

// DefaultMaxEntries bounds a cache created without an explicit limit.
const DefaultMaxEntries = 4096

type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// TTLCache is a map whose entries expire and whose size is bounded.
//
// Both properties matter: an entry that is merely ignored once stale still
// occupies memory forever, and a map keyed by tenant, template or provider
// grows with the workload unless something evicts.
//
// It is safe for concurrent use.
type TTLCache[V any] struct {
	mu         sync.Mutex
	entries    map[string]entry[V]
	ttl        time.Duration
	maxEntries int
}

// New returns a cache whose entries live for ttl.
func New[V any](ttl time.Duration) *TTLCache[V] {
	return NewWithLimit[V](ttl, DefaultMaxEntries)
}

// NewWithLimit returns a cache holding at most maxEntries entries.
func NewWithLimit[V any](ttl time.Duration, maxEntries int) *TTLCache[V] {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	return &TTLCache[V]{
		entries:    make(map[string]entry[V]),
		ttl:        ttl,
		maxEntries: maxEntries,
	}
}

// Get returns the cached value if it is present and unexpired.
func (c *TTLCache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.entries, key)
		var zero V
		return zero, false
	}
	return e.value, true
}

// Put stores a value, evicting expired entries first and refusing to grow past
// the limit.
func (c *TTLCache[V]) Put(key string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		c.evictExpiredLocked()
		if len(c.entries) >= c.maxEntries {
			// Still full of live entries: skip the write rather than grow. The
			// caller falls back to fetching, which is correct, just slower.
			return
		}
	}

	c.entries[key] = entry[V]{value: value, expiresAt: time.Now().Add(c.ttl)}
}

// Delete drops a key, used when the underlying value is known to have changed.
func (c *TTLCache[V]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// Len reports the number of entries currently held, expired ones included.
func (c *TTLCache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *TTLCache[V]) evictExpiredLocked() {
	now := time.Now()
	for key, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, key)
		}
	}
}
