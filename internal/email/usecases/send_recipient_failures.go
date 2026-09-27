package usecases

import (
	"fmt"
	"strings"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/pkg/emailutil"
)

// RecipientFailure is one recipient's outcome in a delivery pass that failed
// for them, classified on that recipient's own error.
type RecipientFailure struct {
	Recipient string
	Err       error
	Class     emailutil.Classification
}

// RecipientFailuresError reports a delivery pass in which at least one
// recipient failed, with every failure classified on its own.
//
// It replaces classifying the pass as a whole. That used to happen in two
// ways, both wrong:
//
//   - When only some recipients failed, the pass reported success, the worker
//     deleted the outbox row, and a recipient that had failed with "try again
//     later" was never tried again -- while its last event read DEFERRED.
//   - When all of them failed, the worker classified the joined error string
//     once and recorded that verdict for every recipient. One "550 user
//     unknown" made the whole string a hard bounce, so a co-recipient whose
//     only problem was a 421 was recorded as hard-bounced -- and a hard bounce
//     suppresses, so they were suppressed for good and never got the message.
//
// Each recipient now gets its own verdict, and only its own.
type RecipientFailuresError struct {
	Failures []RecipientFailure
	// Delivered counts recipients delivered in this pass. Recipients delivered
	// in an earlier pass were skipped and are not counted.
	Delivered int
}

// Error reads as the errors.Join it replaced, one line per recipient, so
// LastError on an outbox row says the same thing it always did.
func (e *RecipientFailuresError) Error() string {
	lines := make([]string, 0, len(e.Failures))
	for _, f := range e.Failures {
		lines = append(lines, fmt.Sprintf("failed to deliver to %s: %v", f.Recipient, f.Err))
	}
	return strings.Join(lines, "\n")
}

// Unwrap exposes each recipient's cause, so errors.Is and errors.As reach them
// as they did through errors.Join.
func (e *RecipientFailuresError) Unwrap() []error {
	errs := make([]error, 0, len(e.Failures))
	for _, f := range e.Failures {
		errs = append(errs, f.Err)
	}
	return errs
}

// Retryable are the failures worth another attempt.
func (e *RecipientFailuresError) Retryable() []RecipientFailure {
	return e.filter(true)
}

// Permanent are the failures that another attempt cannot change.
func (e *RecipientFailuresError) Permanent() []RecipientFailure {
	return e.filter(false)
}

func (e *RecipientFailuresError) filter(retryable bool) []RecipientFailure {
	var out []RecipientFailure
	for _, f := range e.Failures {
		if f.Class.Retryable == retryable {
			out = append(out, f)
		}
	}
	return out
}

// settledEvents are the outcomes that finish a recipient for good.
//
// A later pass over the same message skips a recipient with one of these, as
// it already skips one that was DELIVERED. Without it, a recipient that
// hard-bounced in an earlier pass -- recorded, and suppressed -- would be tried
// again every time a co-recipient's transient failure kept the message alive.
var settledEvents = map[panmailv1.EmailEventType]bool{
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE:  true,
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT:  true,
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_COMPLAINED:   true,
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED: true,
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED:     true,
	panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DROPPED:      true,
}
