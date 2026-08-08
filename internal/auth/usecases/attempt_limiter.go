package usecases

import (
	"sync"
	"time"
)

// maxTrackedSubjects bounds the limiter's memory. The key is caller-supplied
// (an email address or user id), so without a bound an attacker could grow the
// map without limit simply by attempting logins for random addresses.
const maxTrackedSubjects = 10000

type attemptRecord struct {
	count    int
	lastTry  time.Time
	blockedT time.Time
}

// attemptLimiter counts failed attempts per subject and blocks a subject once
// it exceeds a threshold. It is safe for concurrent use: every read and write
// of a record happens under the limiter's own lock, so records are never
// mutated through a shared pointer.
type attemptLimiter struct {
	mu       sync.Mutex
	records  map[string]*attemptRecord
	max      int
	blockFor time.Duration
}

func newAttemptLimiter(max int, blockFor time.Duration) *attemptLimiter {
	return &attemptLimiter{
		records:  make(map[string]*attemptRecord),
		max:      max,
		blockFor: blockFor,
	}
}

// Blocked reports whether the subject is currently locked out, and for how long.
func (l *attemptLimiter) Blocked(subject string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.records[subject]
	if !ok || rec.count < l.max {
		return false, 0
	}

	remaining := time.Until(rec.blockedT)
	if remaining <= 0 {
		delete(l.records, subject)
		return false, 0
	}
	return true, remaining
}

// Fail records a failed attempt for the subject.
func (l *attemptLimiter) Fail(subject string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.evictExpiredLocked()

	rec, ok := l.records[subject]
	if !ok {
		if len(l.records) >= maxTrackedSubjects {
			// Full even after eviction: drop the new subject rather than grow
			// without bound. Legitimate lockouts already tracked are preserved.
			return
		}
		rec = &attemptRecord{}
		l.records[subject] = rec
	}

	rec.count++
	rec.lastTry = time.Now()
	if rec.count >= l.max {
		rec.blockedT = time.Now().Add(l.blockFor)
	}
}

// Reset clears the subject's history, called after a successful attempt.
func (l *attemptLimiter) Reset(subject string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.records, subject)
}

// evictExpiredLocked drops records that can no longer block anyone. The caller
// must hold the lock.
func (l *attemptLimiter) evictExpiredLocked() {
	cutoff := time.Now().Add(-l.blockFor)
	for subject, rec := range l.records {
		if rec.lastTry.Before(cutoff) {
			delete(l.records, subject)
		}
	}
}
