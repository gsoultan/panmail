// Package ratelimit caps how fast a tenant can send.
//
// Nothing capped it before. A runaway loop or a leaked API key could send
// without bound, and because tenants share a provider and a sending IP the
// damage is not confined to the tenant that caused it: a sudden spike gets the
// shared domain rate-limited or blocklisted by receiving providers, and every
// other tenant's mail degrades behind it. Reputation is slow to earn and fast
// to lose, which makes the ceiling worth having even when nobody is abusing
// anything — most runaway sends are bugs, not attacks.
package ratelimit

import (
	"sync"
	"time"
)

// Limit describes a tenant's allowance. A zero or negative PerMinute means
// unlimited, which is what an unconfigured tenant gets: switching this on
// should not silently start dropping mail for everyone already running.
type Limit struct {
	PerMinute int
	// Burst is how much can go at once before the rate binds. Sending is
	// naturally bursty — a campaign is queued in one go — so a bucket that
	// only ever holds one minute's worth would reject the normal case.
	Burst int
}

// Unlimited reports whether the limit imposes any ceiling at all.
func (l Limit) Unlimited() bool { return l.PerMinute <= 0 }

func (l Limit) capacity() float64 {
	if l.Burst > 0 {
		return float64(l.Burst)
	}
	// Without an explicit burst, allow one minute's worth to go at once.
	return float64(l.PerMinute)
}

type bucket struct {
	tokens   float64
	capacity float64
	perSec   float64
	// When the bucket was last refilled, and when it was last asked about —
	// the second is for eviction, not accounting.
	refilled time.Time
	seen     time.Time
}

// Limiter hands out send allowances per tenant.
//
// The state is per process. A deployment running several gateways therefore
// permits roughly N times the configured rate, which is a deliberate trade:
// a shared counter would mean a round trip to the database on the hot path of
// every send, and the purpose here is to stop runaway sends and protect a
// shared reputation, not to bill anyone by the message. Anything that needs to
// be exact belongs in the provider's own quota.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	now func() time.Time

	// A bucket per tenant, and tenants come and go. Without a ceiling and an
	// eviction this map only grows for the life of the process.
	maxBuckets int
	idleTTL    time.Duration
}

const (
	defaultMaxBuckets = 10_000
	defaultIdleTTL    = 15 * time.Minute
)

func New() *Limiter {
	return &Limiter{
		buckets:    make(map[string]*bucket),
		now:        time.Now,
		maxBuckets: defaultMaxBuckets,
		idleTTL:    defaultIdleTTL,
	}
}

// NewWithClock builds a limiter driven by an injected clock, so the refill
// behaviour can be tested without sleeping through it.
func NewWithClock(now func() time.Time) *Limiter {
	l := New()
	l.now = now
	return l
}

// Allow charges `cost` against the tenant's allowance.
//
// The cost is the number of messages about to be handed to a provider, not the
// number of API calls: one request addressed to fifty recipients is fifty
// deliveries, and it is deliveries that receiving providers count when they
// decide what a domain's reputation is.
//
// When it refuses, retryAfter says how long until enough tokens exist for this
// cost — so a caller can defer rather than guess, and an API client can be told
// something better than "try again".
func (l *Limiter) Allow(tenantID string, limit Limit, cost int) (allowed bool, retryAfter time.Duration) {
	if limit.Unlimited() {
		return true, 0
	}
	if cost <= 0 {
		cost = 1
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b := l.bucketFor(tenantID, limit, now)

	// Refill for the time that has passed, capped at the bucket's size.
	elapsed := now.Sub(b.refilled).Seconds()
	if elapsed > 0 {
		b.tokens = minFloat(b.capacity, b.tokens+elapsed*b.perSec)
		b.refilled = now
	}
	b.seen = now

	want := float64(cost)

	// A cost larger than the bucket could never be satisfied, so waiting for it
	// would block forever. Let it through rather than wedge the queue on one
	// oversized message, and let the ceiling apply to what follows.
	if want > b.capacity {
		b.tokens = 0
		return true, 0
	}

	if b.tokens >= want {
		b.tokens -= want
		return true, 0
	}

	shortfall := want - b.tokens
	return false, time.Duration(shortfall / b.perSec * float64(time.Second))
}

// bucketFor returns the tenant's bucket, creating or re-rating it as needed.
// Callers hold the lock.
func (l *Limiter) bucketFor(tenantID string, limit Limit, now time.Time) *bucket {
	perSec := float64(limit.PerMinute) / 60
	capacity := limit.capacity()

	if b, ok := l.buckets[tenantID]; ok {
		// A limit raised or lowered while the process is running takes effect
		// immediately; holding the old rate until restart would make the
		// setting look broken.
		if b.perSec != perSec || b.capacity != capacity {
			b.perSec = perSec
			b.capacity = capacity
			b.tokens = minFloat(b.tokens, capacity)
		}
		return b
	}

	l.evictIfNeeded(now)

	b := &bucket{
		tokens:   capacity,
		capacity: capacity,
		perSec:   perSec,
		refilled: now,
		seen:     now,
	}
	l.buckets[tenantID] = b
	return b
}

// evictIfNeeded keeps the map bounded. Callers hold the lock.
func (l *Limiter) evictIfNeeded(now time.Time) {
	if len(l.buckets) < l.maxBuckets {
		return
	}

	// Idle buckets first: a tenant that has not sent in a while has a full
	// bucket anyway, so dropping it changes nothing.
	for id, b := range l.buckets {
		if now.Sub(b.seen) > l.idleTTL {
			delete(l.buckets, id)
		}
	}
	if len(l.buckets) < l.maxBuckets {
		return
	}

	// Still full, so every tenant is active. Drop the least recently used,
	// which costs that tenant a reset allowance rather than costing everyone
	// an unbounded map.
	var oldest string
	var oldestSeen time.Time
	for id, b := range l.buckets {
		if oldest == "" || b.seen.Before(oldestSeen) {
			oldest, oldestSeen = id, b.seen
		}
	}
	delete(l.buckets, oldest)
}

// Forget drops a tenant's state, for when the tenant itself is deleted.
func (l *Limiter) Forget(tenantID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, tenantID)
}

// Tracked reports how many tenants currently hold state, for tests and metrics.
func (l *Limiter) Tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
