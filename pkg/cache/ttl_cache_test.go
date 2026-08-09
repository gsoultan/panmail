package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestTTLCacheExpires(t *testing.T) {
	c := New[string](20 * time.Millisecond)
	c.Put("k", "v")

	if got, ok := c.Get("k"); !ok || got != "v" {
		t.Fatalf("Get() = %q, %v; want \"v\", true", got, ok)
	}

	time.Sleep(40 * time.Millisecond)

	if _, ok := c.Get("k"); ok {
		t.Error("expected the entry to have expired")
	}
	if c.Len() != 0 {
		t.Errorf("expected the expired entry to be dropped, %d remain", c.Len())
	}
}

// A cache keyed by caller-influenced values must not grow without bound.
func TestTTLCacheIsBounded(t *testing.T) {
	const limit = 32
	c := NewWithLimit[int](time.Hour, limit)

	for i := range 10 * limit {
		c.Put(fmt.Sprintf("key-%d", i), i)
	}

	if c.Len() > limit {
		t.Errorf("cache grew to %d entries; limit is %d", c.Len(), limit)
	}
}

func TestTTLCacheEvictsExpiredToMakeRoom(t *testing.T) {
	c := NewWithLimit[int](20*time.Millisecond, 4)

	for i := range 4 {
		c.Put(fmt.Sprintf("old-%d", i), i)
	}
	time.Sleep(40 * time.Millisecond)

	c.Put("fresh", 99)

	if got, ok := c.Get("fresh"); !ok || got != 99 {
		t.Error("expected a fresh entry to replace expired ones")
	}
}

func TestTTLCacheDelete(t *testing.T) {
	c := New[string](time.Hour)
	c.Put("k", "v")
	c.Delete("k")

	if _, ok := c.Get("k"); ok {
		t.Error("expected the entry to be gone")
	}
}

func TestTTLCacheIsConcurrencySafe(t *testing.T) {
	c := New[int](time.Minute)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i%10)
			c.Put(key, i)
			c.Get(key)
			c.Len()
		}(i)
	}
	wg.Wait()
}
