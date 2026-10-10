package usecases

import (
	"container/list"
	"sync"
	"time"
)

// maxTrackedSubjects bounds the limiter's memory. The key is caller-supplied
// (an email address or user id), so without a bound an attacker could grow the
// map without limit simply by attempting logins for random addresses.
const maxTrackedSubjects = 10000

// sweepInterval is how often expired records are swept out. Sweeping is a walk
// of the whole map under the limiter's lock, so it runs on a timer rather than
// on every failure: per failure it made each wrong password cost a scan of
// up to ten thousand records while every other sign-in waited.
const sweepInterval = time.Minute

// evictionProbe is how many of the least recently used records are examined
// for one that is safe to drop when the limiter is full. Bounded so that
// eviction stays O(1) however full the map is.
const evictionProbe = 64

type attemptRecord struct {
	subject string

	// failures counts completed failed attempts. inFlight counts attempts that
	// have been admitted and not yet finished; both count against the budget,
	// so concurrent requests cannot all be admitted before any of them fails.
	failures int
	inFlight int

	lastTry      time.Time
	blockedUntil time.Time

	// elem is this record's place in the recency list.
	elem *list.Element
}

func (r *attemptRecord) blocked(now time.Time) bool {
	return now.Before(r.blockedUntil)
}

// attemptLimiter counts failed attempts per subject and blocks a subject once
// it exceeds a threshold. It is safe for concurrent use: every read and write
// of a record happens under the limiter's own lock.
type attemptLimiter struct {
	mu       sync.Mutex
	records  map[string]*attemptRecord
	recency  *list.List // front is least recently used
	max      int
	capacity int
	blockFor time.Duration

	lastSweep time.Time
	now       func() time.Time
}

func newAttemptLimiter(max int, blockFor time.Duration) *attemptLimiter {
	return &attemptLimiter{
		records:  make(map[string]*attemptRecord),
		recency:  list.New(),
		max:      max,
		capacity: maxTrackedSubjects,
		blockFor: blockFor,
		now:      time.Now,
	}
}

// attempt is an admitted attempt holding one unit of its subject's budget.
// Exactly one of Succeeded, Failed or Abandon settles it; the later calls are
// no-ops, so `defer a.Abandon()` is a safe catch-all.
type attempt struct {
	limiter *attemptLimiter
	rec     *attemptRecord
	settled bool
}

// Begin admits an attempt for the subject, or reports that the subject is
// locked out and for how long.
//
// The check and the reservation are one step under the lock. When they were
// two — Blocked() before the work, Fail() after it — every concurrent request
// passed the check before any of them failed, and a burst of 200 password
// guesses had 46 checked against a limit of 5.
func (l *attemptLimiter) Begin(subject string) (*attempt, bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.maybeSweepLocked(now)

	rec, ok := l.records[subject]
	if ok {
		l.expireLocked(rec, now)
	}
	if ok && rec.blocked(now) {
		return nil, true, rec.blockedUntil.Sub(now)
	}
	if ok && rec.failures+rec.inFlight >= l.max {
		// Not locked yet, but the attempts already running would use up the
		// budget if they fail. Admitting more is exactly the burst this
		// prevents; the caller may retry once they finish.
		return nil, true, 0
	}

	if !ok {
		rec = l.insertLocked(subject)
	}
	rec.inFlight++
	rec.lastTry = now
	l.recency.MoveToBack(rec.elem)
	return &attempt{limiter: l, rec: rec}, false, 0
}

// Succeeded clears the subject's history.
func (a *attempt) Succeeded() {
	a.settle(func(l *attemptLimiter, rec *attemptRecord) {
		l.removeLocked(rec)
	})
}

// Failed records a failed attempt, locking the subject once it has used its
// budget.
func (a *attempt) Failed() {
	a.settle(func(l *attemptLimiter, rec *attemptRecord) {
		rec.failures++
		if rec.failures >= l.max {
			rec.blockedUntil = l.now().Add(l.blockFor)
		}
		// Evicted while the attempt ran: put it back, so the failure counts.
		if l.records[rec.subject] != rec {
			if existing, ok := l.records[rec.subject]; ok {
				existing.failures += rec.failures
				if existing.failures >= l.max {
					existing.blockedUntil = l.now().Add(l.blockFor)
				}
				return
			}
			l.makeRoomLocked()
			rec.elem = l.recency.PushBack(rec)
			l.records[rec.subject] = rec
		}
	})
}

// Abandon releases the reservation without counting it either way, for an
// attempt that ended before it tested anything.
func (a *attempt) Abandon() {
	a.settle(func(*attemptLimiter, *attemptRecord) {})
}

func (a *attempt) settle(fn func(*attemptLimiter, *attemptRecord)) {
	if a == nil {
		return
	}
	l := a.limiter
	l.mu.Lock()
	defer l.mu.Unlock()
	if a.settled {
		return
	}
	a.settled = true
	a.rec.inFlight--
	fn(l, a.rec)
}

// expireLocked forgets failures that can no longer lead to a lockout: a block
// that has run out, or failures older than one block period.
func (l *attemptLimiter) expireLocked(rec *attemptRecord, now time.Time) {
	if !rec.blockedUntil.IsZero() && !rec.blocked(now) {
		rec.failures = 0
		rec.blockedUntil = time.Time{}
	}
	if rec.failures > 0 && rec.blockedUntil.IsZero() && now.Sub(rec.lastTry) > l.blockFor {
		rec.failures = 0
	}
}

// insertLocked tracks a new subject, making room first if the limiter is full.
//
// It used to refuse to track a new subject once full. That made the bound an
// off switch: flood the limiter with junk addresses and the real target was
// never tracked, so never locked.
func (l *attemptLimiter) insertLocked(subject string) *attemptRecord {
	l.makeRoomLocked()
	rec := &attemptRecord{subject: subject}
	rec.elem = l.recency.PushBack(rec)
	l.records[subject] = rec
	return rec
}

// makeRoomLocked evicts until there is space for one more record.
//
// It prefers the least recently used record that is neither locked out nor
// mid-attempt, so that the cheapest way to release a lockout is not to make
// the limiter forget it. Pushing out a lockout takes the attacker a full
// map's worth of locked subjects of their own, five failures each.
func (l *attemptLimiter) makeRoomLocked() {
	if len(l.records) < l.capacity {
		return
	}
	now := l.now()
	probe := 0
	for e := l.recency.Front(); e != nil && probe < evictionProbe; e = e.Next() {
		rec := e.Value.(*attemptRecord)
		if !rec.blocked(now) && rec.inFlight == 0 {
			l.removeLocked(rec)
			return
		}
		probe++
	}
	l.removeLocked(l.recency.Front().Value.(*attemptRecord))
}

func (l *attemptLimiter) removeLocked(rec *attemptRecord) {
	if l.records[rec.subject] == rec {
		delete(l.records, rec.subject)
	}
	if rec.elem != nil {
		l.recency.Remove(rec.elem)
		rec.elem = nil
	}
}

// maybeSweepLocked drops records that can no longer block anyone, at most once
// per sweepInterval.
func (l *attemptLimiter) maybeSweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < sweepInterval {
		return
	}
	l.lastSweep = now

	cutoff := now.Add(-l.blockFor)
	for _, rec := range l.records {
		if rec.inFlight == 0 && !rec.blocked(now) && rec.lastTry.Before(cutoff) {
			l.removeLocked(rec)
		}
	}
}
