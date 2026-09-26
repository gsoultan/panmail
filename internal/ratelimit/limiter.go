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

	d := assess(b, cost, now)
	d.apply(b)
	return d.allowed, d.retryAfter
}

// Request is one bucket a send must pay, for AllowAll.
type Request struct {
	// Key identifies the bucket. A tenant id, or a tenant and provider id
	// joined, so one tenant's providers do not share an allowance.
	Key   string
	Limit Limit
}

// AllowAll charges several buckets as one decision: every bucket pays, or none
// does.
//
// Charging them in sequence instead would leak. A send that the tenant's
// allowance admits but a provider's refuses would already have spent a tenant
// token, so a caller retrying against a saturated provider would drain the
// tenant's ceiling on messages that were never accepted — the limiter
// punishing a tenant for a refusal it made itself.
//
// retryAfter is the longest of the refusing buckets': that is when the send
// could next satisfy all of them, and telling a caller the shortest would
// invite an immediate second refusal.
func (l *Limiter) AllowAll(reqs []Request, cost int) (allowed bool, retryAfter time.Duration) {
	if cost <= 0 {
		cost = 1
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	type pending struct {
		b *bucket
		d decision
	}

	// Decide first, mutate second. Refilling is time-based and safe to do
	// either way, but the oversized-cost case empties a bucket, and doing that
	// before knowing whether a later bucket refuses would charge a send that
	// never went out.
	decisions := make([]pending, 0, len(reqs))
	allowed = true

	for _, req := range reqs {
		if req.Limit.Unlimited() || req.Key == "" {
			continue
		}
		b := l.bucketFor(req.Key, req.Limit, now)
		d := assess(b, cost, now)
		if !d.allowed {
			allowed = false
			if d.retryAfter > retryAfter {
				retryAfter = d.retryAfter
			}
		}
		decisions = append(decisions, pending{b: b, d: d})
	}

	if !allowed {
		return false, retryAfter
	}

	for _, p := range decisions {
		p.d.apply(p.b)
	}
	return true, 0
}

// decision is what a bucket would do about a cost, before it does it.
type decision struct {
	allowed    bool
	retryAfter time.Duration
	// spend is the token count to deduct when applied. Ignored if empty.
	spend float64
	// empty marks the oversized-cost case, which zeroes the bucket rather
	// than deducting from it.
	empty bool
}

func (d decision) apply(b *bucket) {
	if !d.allowed {
		return
	}
	if d.empty {
		b.tokens = 0
		return
	}
	b.tokens -= d.spend
}

// assess refills a bucket for elapsed time and reports what it would do about
// cost, without spending anything. Callers hold the lock.
//
// The refill is applied here rather than deferred: it reflects time that has
// passed, not a charge, so it is correct whether or not the send proceeds.
func assess(b *bucket, cost int, now time.Time) decision {
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
		return decision{allowed: true, empty: true}
	}

	if b.tokens >= want {
		return decision{allowed: true, spend: want}
	}

	shortfall := want - b.tokens
	return decision{retryAfter: time.Duration(shortfall / b.perSec * float64(time.Second))}
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

// Reservation is a claim on future tokens, made by ReserveAll.
//
// It exists for the delivery side, where the answer to "not yet" is not a
// refusal but a time. A send is charged now and told how long until the tokens
// it took would have been there; if that is short it waits and sends, and if
// it is long it Cancels and the message is rescheduled for then.
type Reservation struct {
	l     *Limiter
	delay time.Duration
	held  []held
}

type held struct {
	key string
	// restore is what Cancel gives back: the cost for an ordinary charge, or
	// the whole balance for the oversized case, which empties the bucket.
	restore float64
}

// Delay is how long until every bucket the reservation drew on would have been
// in credit. Zero means the tokens were there.
func (r *Reservation) Delay() time.Duration {
	if r == nil {
		return 0
	}
	return r.delay
}

// Cancel returns the tokens a reservation took.
//
// Giving back exactly what was taken errs on the slow side, never the fast one:
// reservations made after this one computed their delays with it counted, so
// they stay scheduled a little later than they now need to be. That is a pace
// slightly under the ceiling, which is the harmless direction for a limit that
// protects someone else's quota.
func (r *Reservation) Cancel() {
	if r == nil || len(r.held) == 0 {
		return
	}
	r.l.mu.Lock()
	defer r.l.mu.Unlock()

	for _, h := range r.held {
		// Evicted since: there is no debt left to repay, and recreating the
		// bucket here would hand out a fresh burst.
		b, ok := r.l.buckets[h.key]
		if !ok {
			continue
		}
		b.tokens = minFloat(b.capacity, b.tokens+h.restore)
	}
	r.held = nil
}

// ReserveAll charges every bucket now, into debt if need be, and reports how
// long until all of them would be in credit.
//
// It is AllowAll's counterpart for a caller that can wait. Taking the tokens
// immediately is what makes concurrent callers queue: the tenth to reserve
// finds nine charges ahead of it and is told a delay nine tokens long, so a
// batch drawing on one bucket is spread across time at the bucket's rate
// rather than all told to come back at the same moment.
func (l *Limiter) ReserveAll(reqs []Request, cost int) *Reservation {
	if cost <= 0 {
		cost = 1
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	r := &Reservation{l: l}
	want := float64(cost)

	for _, req := range reqs {
		if req.Limit.Unlimited() || req.Key == "" {
			continue
		}
		b := l.bucketFor(req.Key, req.Limit, now)

		elapsed := now.Sub(b.refilled).Seconds()
		if elapsed > 0 {
			b.tokens = minFloat(b.capacity, b.tokens+elapsed*b.perSec)
			b.refilled = now
		}
		b.seen = now

		// The same rule as Allow: a cost the bucket could never hold goes
		// through rather than wedging the queue, and empties the bucket so the
		// ceiling applies to what follows. Waiting for it would be waiting
		// forever.
		if want > b.capacity {
			r.held = append(r.held, held{key: req.Key, restore: maxFloat(b.tokens, 0)})
			b.tokens = 0
			continue
		}

		b.tokens -= want
		r.held = append(r.held, held{key: req.Key, restore: want})

		if b.tokens < 0 {
			wait := time.Duration(-b.tokens / b.perSec * float64(time.Second))
			if wait > r.delay {
				r.delay = wait
			}
		}
	}
	return r
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
