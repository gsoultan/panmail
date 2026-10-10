package usecases

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
)

// failN records n failed attempts for the subject, or reports that it was
// refused before using them all.
func failN(t *testing.T, l *attemptLimiter, subject string, n int) {
	t.Helper()
	for i := range n {
		a, blocked, _ := l.Begin(subject)
		if blocked {
			t.Fatalf("%s blocked after %d of %d failures", subject, i, n)
		}
		a.Failed()
	}
}

func isBlocked(l *attemptLimiter, subject string) bool {
	a, blocked, _ := l.Begin(subject)
	a.Abandon()
	return blocked
}

func TestLimiterLocksOutAtTheLimitAndSuccessClears(t *testing.T) {
	l := newAttemptLimiter(3, time.Minute)

	failN(t, l, "a", 2)
	a, blocked, _ := l.Begin("a")
	if blocked {
		t.Fatal("blocked before the limit")
	}
	a.Succeeded()
	failN(t, l, "a", 2) // the success started a fresh budget

	failN(t, l, "a", 1)
	if !isBlocked(l, "a") {
		t.Fatal("not blocked after reaching the limit")
	}
}

// The bound used to be an off switch: once the map was full, new subjects
// were never tracked, so an attacker who first tried ten thousand junk
// addresses could then guess at the real one without limit.
func TestAFullLimiterStillLocksOutANewSubject(t *testing.T) {
	l := newAttemptLimiter(5, time.Hour)
	l.capacity = 100

	for i := range l.capacity {
		failN(t, l, fmt.Sprintf("junk-%d@example.com", i), 1)
	}

	failN(t, l, "victim@example.com", 5)
	if !isBlocked(l, "victim@example.com") {
		t.Fatal("a subject first seen with the limiter full was never locked out")
	}
	if len(l.records) > l.capacity {
		t.Errorf("limiter grew to %d records; capacity is %d", len(l.records), l.capacity)
	}
}

// Evicting to make room must not become the way to lift a lockout: a flood
// of fresh subjects pushes out unlocked records before locked ones.
func TestAFloodDoesNotEvictALockout(t *testing.T) {
	l := newAttemptLimiter(5, time.Hour)
	l.capacity = 100

	failN(t, l, "victim@example.com", 5)
	for i := range 3 * l.capacity {
		failN(t, l, fmt.Sprintf("junk-%d@example.com", i), 1)
	}

	if !isBlocked(l, "victim@example.com") {
		t.Fatal("flooding the limiter with junk subjects released a lockout")
	}
}

// Concurrent attempts were checked before any of them had failed, so all of
// them were admitted. Admission now reserves budget, so at most the limit
// is ever tested.
func TestConcurrentAttemptsCannotOutrunTheLimit(t *testing.T) {
	l := newAttemptLimiter(5, time.Hour)

	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 200 {
		wg.Go(func() {
			<-start
			a, blocked, _ := l.Begin("victim@example.com")
			if blocked {
				return
			}
			admitted.Add(1)
			time.Sleep(5 * time.Millisecond) // the password check
			a.Failed()
		})
	}
	close(start)
	wg.Wait()

	if got := admitted.Load(); got > 5 {
		t.Errorf("%d concurrent attempts admitted; the limit is 5", got)
	}
	if !isBlocked(l, "victim@example.com") {
		t.Error("not locked out after the limit was used")
	}
}

// The same property end to end: concurrent wrong passwords reach the password
// check at most maxLoginAttempts times.
func TestConcurrentSignInsCheckAtMostTheLimit(t *testing.T) {
	repo := &countingUserRepo{stubUserRepo: newStubUserRepo(newUser(t, ""))}
	usecase, _ := newTestUsecase(t, repo.stubUserRepo)
	usecase.(*authUsecase).repo = repo

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 200 {
		wg.Go(func() {
			<-start
			_, _ = usecase.SignIn(context.Background(), Credentials{Email: "admin@example.com", Password: "wrong"})
		})
	}
	close(start)
	wg.Wait()

	if got := repo.lookups.Load(); got > maxLoginAttempts {
		t.Errorf("%d passwords checked out of 200 concurrent guesses; the limit is %d", got, maxLoginAttempts)
	}
}

// countingUserRepo counts password lookups and makes each take long enough
// for concurrent requests to overlap.
type countingUserRepo struct {
	*stubUserRepo
	lookups atomic.Int32
}

func (r *countingUserRepo) GetByEmail(ctx context.Context, email string) (*entities.User, error) {
	r.lookups.Add(1)
	time.Sleep(2 * time.Millisecond)
	return r.stubUserRepo.GetByEmail(ctx, email)
}

// Expired records are swept on a schedule, not on every failure: a sweep
// walks the whole map under the lock that every sign-in takes.
func TestExpiredRecordsAreSweptPeriodically(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newAttemptLimiter(5, time.Minute)
	l.now = func() time.Time { return now }

	for i := range 10 {
		failN(t, l, fmt.Sprintf("s-%d", i), 1)
	}

	// Long expired, but the last sweep was moments ago: nothing is walked.
	now = now.Add(2 * time.Minute)
	l.lastSweep = now.Add(-sweepInterval / 2)
	failN(t, l, "trigger", 1)
	if got := len(l.records); got != 11 {
		t.Fatalf("%d records after a failure inside the sweep interval, want 11 — a failure swept the map", got)
	}

	// Once the interval has passed, the next attempt sweeps.
	now = now.Add(sweepInterval + time.Second)
	failN(t, l, "trigger-2", 1)
	if got := len(l.records); got != 1 {
		t.Errorf("%d records after the sweep interval, want only the newest", got)
	}
}

// A lockout that has run out is forgotten, rather than locking again on the
// next single failure.
func TestAnExpiredLockoutStartsAFreshBudget(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newAttemptLimiter(3, time.Minute)
	l.now = func() time.Time { return now }

	failN(t, l, "a", 3)
	if !isBlocked(l, "a") {
		t.Fatal("expected a lockout")
	}
	now = now.Add(2 * time.Minute)
	failN(t, l, "a", 2)
	if isBlocked(l, "a") {
		t.Error("two failures after an expired lockout locked the subject again")
	}
}
