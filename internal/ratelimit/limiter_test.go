package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// A fixed clock, so refill behaviour is exercised without sleeping through it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestLimiter() (*Limiter, *clock) {
	c := newClock()
	return NewWithClock(c.Now), c
}

const tenant = "tenant-a"

// A tenant with nothing configured must keep sending exactly as before.
// Switching this on should not quietly start dropping everyone's mail.
func TestAnUnconfiguredTenantIsNotLimited(t *testing.T) {
	l, _ := newTestLimiter()

	for i := range 1000 {
		if allowed, _ := l.Allow(tenant, Limit{}, 1); !allowed {
			t.Fatalf("send %d refused for a tenant with no limit set", i)
		}
	}
	if l.Tracked() != 0 {
		t.Error("an unlimited tenant should not occupy a bucket")
	}
}

func TestABurstIsAllowedThenTheRateBinds(t *testing.T) {
	l, _ := newTestLimiter()
	limit := Limit{PerMinute: 60, Burst: 10}

	for i := range 10 {
		if allowed, _ := l.Allow(tenant, limit, 1); !allowed {
			t.Fatalf("send %d refused inside the burst", i+1)
		}
	}

	allowed, retryAfter := l.Allow(tenant, limit, 1)
	if allowed {
		t.Error("the eleventh send should exceed a burst of ten")
	}
	if retryAfter <= 0 {
		t.Error("a refusal has to say when to come back, or the caller can only guess")
	}
}

func TestTokensRefillOverTime(t *testing.T) {
	l, c := newTestLimiter()
	limit := Limit{PerMinute: 60, Burst: 10}

	for range 10 {
		l.Allow(tenant, limit, 1)
	}
	if allowed, _ := l.Allow(tenant, limit, 1); allowed {
		t.Fatal("expected the bucket to be empty")
	}

	// 60 a minute is one a second.
	c.advance(3 * time.Second)

	for i := range 3 {
		if allowed, _ := l.Allow(tenant, limit, 1); !allowed {
			t.Errorf("send %d refused after three seconds of refill", i+1)
		}
	}
	if allowed, _ := l.Allow(tenant, limit, 1); allowed {
		t.Error("a fourth send should not have refilled yet")
	}
}

func TestRefillStopsAtTheBurstCeiling(t *testing.T) {
	l, c := newTestLimiter()
	limit := Limit{PerMinute: 60, Burst: 10}

	l.Allow(tenant, limit, 10)
	// An hour of idling must not bank an hour's worth of sends, or the
	// ceiling means nothing after any quiet period.
	c.advance(time.Hour)

	for i := range 10 {
		if allowed, _ := l.Allow(tenant, limit, 1); !allowed {
			t.Fatalf("send %d refused after a long idle", i+1)
		}
	}
	if allowed, _ := l.Allow(tenant, limit, 1); allowed {
		t.Error("idling banked more than the burst")
	}
}

// A request addressed to fifty people is fifty deliveries, and deliveries are
// what receiving providers count when they judge a domain.
func TestTheCostIsCharged(t *testing.T) {
	l, _ := newTestLimiter()
	limit := Limit{PerMinute: 600, Burst: 10}

	if allowed, _ := l.Allow(tenant, limit, 7); !allowed {
		t.Fatal("seven recipients should fit in a burst of ten")
	}
	if allowed, _ := l.Allow(tenant, limit, 4); allowed {
		t.Error("four more should not fit in the three that remain")
	}
	if allowed, _ := l.Allow(tenant, limit, 3); !allowed {
		t.Error("three should fit exactly")
	}
}

// Otherwise a single oversized message can never be sent and the queue wedges
// on it forever, retrying something that can never succeed.
func TestAMessageLargerThanTheBucketIsNotWedged(t *testing.T) {
	l, _ := newTestLimiter()
	limit := Limit{PerMinute: 60, Burst: 10}

	if allowed, _ := l.Allow(tenant, limit, 50); !allowed {
		t.Fatal("a cost above the burst must still go, or it can never go at all")
	}
	// It empties the bucket, so the ceiling still applies to what follows.
	if allowed, _ := l.Allow(tenant, limit, 1); allowed {
		t.Error("an oversized send should leave nothing behind it")
	}
}

func TestTenantsAreLimitedIndependently(t *testing.T) {
	l, _ := newTestLimiter()
	limit := Limit{PerMinute: 60, Burst: 5}

	for range 5 {
		l.Allow("noisy", limit, 1)
	}
	if allowed, _ := l.Allow("noisy", limit, 1); allowed {
		t.Fatal("the noisy tenant should be capped")
	}

	// The whole point: one tenant exhausting its allowance must not stop
	// anyone else sending.
	if allowed, _ := l.Allow("quiet", limit, 1); !allowed {
		t.Error("a second tenant was refused because of the first")
	}
}

func TestRetryAfterMatchesTheShortfall(t *testing.T) {
	l, _ := newTestLimiter()
	limit := Limit{PerMinute: 60, Burst: 10}

	l.Allow(tenant, limit, 10)
	_, retryAfter := l.Allow(tenant, limit, 5)

	// Five tokens short at one a second.
	if retryAfter < 4*time.Second || retryAfter > 6*time.Second {
		t.Errorf("retryAfter = %v, want about 5s", retryAfter)
	}
}

// A raised limit applies through the new refill rate, not by handing over the
// difference at once. Granting the increase immediately would make editing the
// setting a way to mint capacity on demand, which defeats the ceiling.
func TestARaisedLimitAppliesAtTheNewRateWithoutARestart(t *testing.T) {
	l, c := newTestLimiter()

	tight := Limit{PerMinute: 60, Burst: 2}
	l.Allow(tenant, tight, 2)
	if allowed, _ := l.Allow(tenant, tight, 1); allowed {
		t.Fatal("expected the tight limit to bind")
	}

	loose := Limit{PerMinute: 6000, Burst: 100}

	// Nothing is banked the instant the limit changes.
	if allowed, _ := l.Allow(tenant, loose, 50); allowed {
		t.Error("raising the limit handed over the whole increase at once")
	}

	// But the new rate is in force straight away: 6000 a minute is 100 a
	// second, so a second later the higher throughput is there. Under the old
	// rate that second would have bought one send.
	c.advance(time.Second)
	if allowed, _ := l.Allow(tenant, loose, 50); !allowed {
		t.Error("the raised rate did not take effect until a restart")
	}
}

func TestLoweringALimitClampsWhatIsBanked(t *testing.T) {
	l, _ := newTestLimiter()

	l.Allow(tenant, Limit{PerMinute: 6000, Burst: 100}, 1)
	// Dropping to a burst of 5 must not leave 99 tokens banked under the new
	// ceiling.
	tight := Limit{PerMinute: 60, Burst: 5}
	if allowed, _ := l.Allow(tenant, tight, 5); !allowed {
		t.Fatal("five should still fit the new burst")
	}
	if allowed, _ := l.Allow(tenant, tight, 1); allowed {
		t.Error("tokens banked under the old, larger burst survived the change")
	}
}

func TestAnUnsetBurstFallsBackToOneMinutesWorth(t *testing.T) {
	l, _ := newTestLimiter()
	limit := Limit{PerMinute: 30}

	for i := range 30 {
		if allowed, _ := l.Allow(tenant, limit, 1); !allowed {
			t.Fatalf("send %d refused within one minute's worth", i+1)
		}
	}
	if allowed, _ := l.Allow(tenant, limit, 1); allowed {
		t.Error("more than a minute's worth went at once with no burst configured")
	}
}

// A bucket per tenant, and tenants come and go: without eviction this map
// grows for the life of the process.
func TestTheBucketMapIsBounded(t *testing.T) {
	l, c := newTestLimiter()
	l.maxBuckets = 50
	limit := Limit{PerMinute: 60, Burst: 1}

	for i := range 500 {
		l.Allow(string(rune('a'+i%26))+string(rune(i)), limit, 1)
		c.advance(time.Second)
	}

	if l.Tracked() > l.maxBuckets {
		t.Errorf("tracking %d tenants, above the cap of %d", l.Tracked(), l.maxBuckets)
	}
}

func TestAnIdleTenantIsEvictedBeforeAnActiveOne(t *testing.T) {
	l, c := newTestLimiter()
	l.maxBuckets = 2
	limit := Limit{PerMinute: 60, Burst: 5}

	l.Allow("idle", limit, 1)
	c.advance(l.idleTTL + time.Minute)

	l.Allow("active", limit, 1)
	l.Allow("newcomer", limit, 1)

	// The active tenant keeps its bucket; the idle one had a full bucket
	// anyway, so dropping it costs nothing.
	if _, ok := l.buckets["idle"]; ok {
		t.Error("the idle tenant was kept over an active one")
	}
	if _, ok := l.buckets["active"]; !ok {
		t.Error("an active tenant lost its allowance to eviction")
	}
}

func TestForgetDropsATenant(t *testing.T) {
	l, _ := newTestLimiter()
	limit := Limit{PerMinute: 60, Burst: 1}

	l.Allow(tenant, limit, 1)
	if allowed, _ := l.Allow(tenant, limit, 1); allowed {
		t.Fatal("expected the burst to be spent")
	}

	l.Forget(tenant)
	if allowed, _ := l.Allow(tenant, limit, 1); !allowed {
		t.Error("a forgotten tenant should start fresh")
	}
}

// Handlers and the outbox worker charge the same buckets concurrently.
func TestConcurrentCallersDoNotExceedTheAllowance(t *testing.T) {
	l, _ := newTestLimiter()
	limit := Limit{PerMinute: 60, Burst: 100}

	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0

	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if allowed, _ := l.Allow(tenant, limit, 1); allowed {
					mu.Lock()
					granted++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	// The clock never moves, so nothing refills: exactly the burst may pass.
	if granted != 100 {
		t.Errorf("granted %d sends, want exactly the burst of 100", granted)
	}
}

// The leak AllowAll exists to prevent: charging buckets in sequence means a
// send the tenant admits but the provider refuses has already spent a tenant
// token, so retrying against a saturated provider drains the tenant's ceiling
// on messages that were never accepted.
func TestAllowAllChargesNothingWhenOneBucketRefuses(t *testing.T) {
	l := New()

	roomy := Limit{PerMinute: 600, Burst: 600}
	tight := Limit{PerMinute: 60, Burst: 1}

	// Exhaust the provider bucket.
	if ok, _ := l.AllowAll([]Request{{Key: "prov", Limit: tight}}, 1); !ok {
		t.Fatal("the first send should have been admitted")
	}

	// Now a send that needs both is refused by the provider.
	if ok, _ := l.AllowAll([]Request{
		{Key: "tenant", Limit: roomy},
		{Key: "prov", Limit: tight},
	}, 1); ok {
		t.Fatal("a send was admitted while the provider bucket was empty")
	}

	// The tenant must not have paid for it. Its bucket started at 600 and has
	// been asked for one token once, which was refused — so 600 sends must
	// still fit.
	for i := range 600 {
		if ok, _ := l.AllowAll([]Request{{Key: "tenant", Limit: roomy}}, 1); !ok {
			t.Fatalf("tenant bucket was drained by a refused send: failed at %d of 600", i+1)
		}
	}
}

func TestAllowAllChargesEveryBucketWhenAllAllow(t *testing.T) {
	l := New()
	limit := Limit{PerMinute: 60, Burst: 2}

	for i := range 2 {
		if ok, _ := l.AllowAll([]Request{
			{Key: "tenant", Limit: limit},
			{Key: "prov", Limit: limit},
		}, 1); !ok {
			t.Fatalf("send %d should have been admitted", i+1)
		}
	}

	// Both buckets held two tokens, so the third is refused.
	if ok, _ := l.AllowAll([]Request{
		{Key: "tenant", Limit: limit},
		{Key: "prov", Limit: limit},
	}, 1); ok {
		t.Fatal("a third send was admitted from a bucket of two")
	}
}

// Telling a caller the shortest wait invites an immediate second refusal.
func TestAllowAllReportsTheLongestWait(t *testing.T) {
	l := New()

	fast := Limit{PerMinute: 600, Burst: 1} // refills in 100ms
	slow := Limit{PerMinute: 6, Burst: 1}   // refills in 10s

	if ok, _ := l.AllowAll([]Request{{Key: "fast", Limit: fast}, {Key: "slow", Limit: slow}}, 1); !ok {
		t.Fatal("the first send should have been admitted")
	}

	ok, retryAfter := l.AllowAll([]Request{
		{Key: "fast", Limit: fast},
		{Key: "slow", Limit: slow},
	}, 1)
	if ok {
		t.Fatal("a second send was admitted with both buckets empty")
	}
	if retryAfter < 5*time.Second {
		t.Fatalf("retryAfter = %v, want the slow bucket's wait, not the fast one's", retryAfter)
	}
}

// An unlimited bucket takes part in the decision without constraining it, so a
// provider with no ceiling does not stop the tenant's from being charged.
func TestAllowAllSkipsUnlimitedAndEmptyKeys(t *testing.T) {
	l := New()
	limit := Limit{PerMinute: 60, Burst: 1}

	ok, _ := l.AllowAll([]Request{
		{Key: "tenant", Limit: limit},
		{Key: "prov", Limit: Limit{}}, // unlimited
		{Key: "", Limit: limit},       // no bucket to charge
	}, 1)
	if !ok {
		t.Fatal("the send should have been admitted")
	}

	// The tenant bucket was charged, so the next is refused.
	if ok, _ := l.AllowAll([]Request{{Key: "tenant", Limit: limit}}, 1); ok {
		t.Fatal("the tenant bucket was not charged")
	}

	// The unlimited provider never got a bucket.
	if l.Tracked() != 1 {
		t.Errorf("tracked %d buckets, want 1", l.Tracked())
	}
}

// A cost bigger than a bucket is let through rather than wedging the queue,
// and that path empties the bucket. It must not empty it when a different
// bucket refuses the send, because then a message that never went out has
// spent someone's whole allowance.
func TestAllowAllDoesNotEmptyABucketForARefusedSend(t *testing.T) {
	l := New()

	// Capacity 1, so a cost of 3 is oversized here and takes the let-through
	// path that zeroes the bucket.
	small := Limit{PerMinute: 60, Burst: 1}
	// Capacity 10, so a cost of 3 is an ordinary charge it can refuse.
	other := Limit{PerMinute: 60, Burst: 10}

	if ok, _ := l.AllowAll([]Request{{Key: "other", Limit: other}}, 10); !ok {
		t.Fatal("the draining send should have been admitted")
	}

	if ok, _ := l.AllowAll([]Request{
		{Key: "small", Limit: small},
		{Key: "other", Limit: other},
	}, 3); ok {
		t.Fatal("a send was admitted while the other bucket was empty")
	}

	// "small" must still hold its token.
	if ok, _ := l.AllowAll([]Request{{Key: "small", Limit: small}}, 1); !ok {
		t.Fatal("the refused send emptied a bucket it never should have charged")
	}
}

func reserveClock() (*Limiter, *time.Time) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	l := NewWithClock(func() time.Time { return now })
	return l, &now
}

// Within the burst there is nothing to wait for.
func TestReserveAllIsImmediateWithinTheBurst(t *testing.T) {
	l, _ := reserveClock()
	limit := Limit{PerMinute: 60, Burst: 3}

	for i := range 3 {
		if d := l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 1).Delay(); d != 0 {
			t.Fatalf("reservation %d delayed %v inside the burst", i+1, d)
		}
	}
}

// The property the delivery side depends on: concurrent callers are spread
// across time at the bucket's rate, rather than all told to come back at once.
// At 60 a minute a token is a second, so the queue is one second apart.
func TestReserveAllSpacesCallersAtTheRate(t *testing.T) {
	l, _ := reserveClock()
	limit := Limit{PerMinute: 60, Burst: 1}

	var delays []time.Duration
	for range 4 {
		delays = append(delays, l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 1).Delay())
	}

	want := []time.Duration{0, time.Second, 2 * time.Second, 3 * time.Second}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("delays = %v, want %v", delays, want)
		}
	}
}

// Cancelling gives the tokens back, so a rescheduled send does not hold a slot
// it is not going to use.
func TestCancelReturnsTheTokens(t *testing.T) {
	l, _ := reserveClock()
	limit := Limit{PerMinute: 60, Burst: 1}

	first := l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 1)
	second := l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 1)
	if second.Delay() != time.Second {
		t.Fatalf("second delay = %v, want 1s", second.Delay())
	}
	second.Cancel()

	// With the second given back, the next caller queues behind the first
	// only, not behind both.
	if d := l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 1).Delay(); d != time.Second {
		t.Fatalf("delay after cancel = %v, want 1s", d)
	}
	_ = first
}

// Giving back more than was taken would let a cancelled reservation mint a
// burst.
func TestCancelNeverRaisesABucketAboveItsCapacity(t *testing.T) {
	l, now := reserveClock()
	limit := Limit{PerMinute: 60, Burst: 2}

	r := l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 1)
	// Long enough that the bucket refills to full on the next touch.
	*now = now.Add(time.Hour)
	l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 0).Cancel()
	r.Cancel()

	for i := range 2 {
		if d := l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 1).Delay(); d != 0 {
			t.Fatalf("reservation %d delayed %v; capacity is 2", i+1, d)
		}
	}
	if d := l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 1).Delay(); d == 0 {
		t.Fatal("a third immediate reservation from a bucket of two: Cancel overfilled it")
	}
}

// Across several buckets the delay is the longest: that is when the send could
// satisfy all of them.
func TestReserveAllReportsTheLongestDelay(t *testing.T) {
	l, _ := reserveClock()
	fast := Limit{PerMinute: 600, Burst: 1} // a token every 100ms
	slow := Limit{PerMinute: 6, Burst: 1}   // a token every 10s

	l.ReserveAll([]Request{{Key: "fast", Limit: fast}, {Key: "slow", Limit: slow}}, 1)
	d := l.ReserveAll([]Request{{Key: "fast", Limit: fast}, {Key: "slow", Limit: slow}}, 1).Delay()
	if d != 10*time.Second {
		t.Fatalf("delay = %v, want the slow bucket's 10s", d)
	}
}

// Same rule as Allow: a cost the bucket could never hold goes through at once
// rather than waiting forever, and Cancel puts back what it took.
func TestReserveAllLetsAnOversizedCostThrough(t *testing.T) {
	l, _ := reserveClock()
	limit := Limit{PerMinute: 60, Burst: 5}

	r := l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 50)
	if r.Delay() != 0 {
		t.Fatalf("oversized cost delayed %v, want immediate", r.Delay())
	}
	r.Cancel()

	for i := range 5 {
		if d := l.ReserveAll([]Request{{Key: "p", Limit: limit}}, 1).Delay(); d != 0 {
			t.Fatalf("after cancelling an oversized reservation, reservation %d delayed %v", i+1, d)
		}
	}
}

func TestReserveAllSkipsUnlimitedBuckets(t *testing.T) {
	l, _ := reserveClock()
	r := l.ReserveAll([]Request{{Key: "p", Limit: Limit{}}, {Key: "", Limit: Limit{PerMinute: 1}}}, 100)
	if r.Delay() != 0 || l.Tracked() != 0 {
		t.Fatalf("delay = %v, tracked = %d; want no buckets and no wait", r.Delay(), l.Tracked())
	}
	r.Cancel() // must not panic on an empty reservation
}
