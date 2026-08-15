package usecases

import (
	"testing"
	"time"

	"github.com/gsoultan/panmail/pkg/emailutil"
)

// When a message stops being retried.
//
// A queue whose messages never reach a terminal state never drains: the rows
// stay, every worker pass reclaims them, and the outbox grows for as long as
// the failure lasts. The retention sweep only removes rows that have already
// given up, so if nothing gives up, nothing is ever swept either.
//
// The rule lives in one small function and nothing exercised it.

var pattern = []string{"5m", "15m", "30m"}

func retryable() emailutil.Classification { return emailutil.Classification{Retryable: true} }
func permanent() emailutil.Classification { return emailutil.Classification{Retryable: false} }

func TestATransientFailureIsRetriedOnTheSchedule(t *testing.T) {
	for i, want := range []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute} {
		// RetryCount is incremented before the delay is chosen, so the first
		// failure asks with 1 and takes the first entry.
		attempt := i + 1
		delay, ok := nextRetryDelay(retryable(), attempt, pattern)
		if !ok {
			t.Fatalf("attempt %d was not retried; the schedule has %d entries", attempt, len(pattern))
		}
		if delay != want {
			t.Errorf("attempt %d waits %v, want %v", attempt, delay, want)
		}
	}
}

// The end of the schedule has to be the end. One-past is the boundary that
// decides whether a queue drains or grows forever.
func TestTheScheduleRunsOut(t *testing.T) {
	if _, ok := nextRetryDelay(retryable(), len(pattern)+1, pattern); ok {
		t.Error("a message was retried past the end of its schedule; nothing would ever " +
			"reach a terminal state and the outbox would grow without bound")
	}
}

// A permanent failure — a rejected address, a refused relay — must not consume
// the schedule at all. Retrying it delays every other message in the batch and
// tells the provider the same bad thing repeatedly, which is what damages a
// sending reputation.
func TestAPermanentFailureIsNotRetriedAtAll(t *testing.T) {
	if _, ok := nextRetryDelay(permanent(), 1, pattern); ok {
		t.Error("a permanent failure was scheduled for retry")
	}
}

// An operator typing "5 minutes" into the retry pattern should not cause the
// message to be dropped. Falling back is deliberate: a mistyped duration is a
// configuration error, and losing mail over it would be worse than waiting a
// slightly wrong amount of time.
func TestAMistypedDurationFallsBackRatherThanDroppingTheMessage(t *testing.T) {
	broken := []string{"5 minutes", "later"}

	delay, ok := nextRetryDelay(retryable(), 1, broken)
	if !ok {
		t.Fatal("a message was dropped because its retry pattern did not parse")
	}
	if delay <= 0 {
		t.Errorf("fallback delay %v would retry immediately, in a loop", delay)
	}

	// And the fallback still grows, so a persistent failure backs off rather
	// than hammering.
	second, ok := nextRetryDelay(retryable(), 2, broken)
	if !ok {
		t.Fatal("the second attempt was dropped")
	}
	if second <= delay {
		t.Errorf("fallback does not back off: attempt 1 %v, attempt 2 %v", delay, second)
	}
}

// An empty pattern means no retries, not unlimited ones. A tenant configured
// with an empty list must not produce messages that are reclaimed forever.
func TestAnEmptyScheduleMeansNoRetries(t *testing.T) {
	if _, ok := nextRetryDelay(retryable(), 1, nil); ok {
		t.Error("an empty retry pattern allowed a retry; with no schedule to run out of, " +
			"the message would never reach a terminal state")
	}
}
