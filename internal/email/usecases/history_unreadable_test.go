package usecases

import (
	"errors"
	"testing"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
)

// The per-recipient idempotency is only as good as the history it reads. When
// that read failed, the error was logged and the pass carried on believing
// nobody had been sent to, so a retry of a partly delivered message delivered
// a second copy to everyone the first pass had already reached.
//
// A pass that cannot tell who is finished must not send to anyone. It fails,
// and the worker tries again later.
func TestAPassThatCannotReadItsHistorySendsNothing(t *testing.T) {
	h := newOutcomeHarness(t, "ok@example.com", "busy@example.com")
	h.replies["busy@example.com"] = []error{errors.New(softBounce)}

	h.pass() // ok@ delivered, busy@ deferred

	h.events.listErr = errors.New("event store unavailable")
	row := h.pass()

	if n := h.attempts["ok@example.com"]; n != 1 {
		t.Errorf("ok@ was sent %d times; a pass that could not read who was delivered sent to them again", n)
	}
	if n := h.attempts["busy@example.com"]; n != 1 {
		t.Errorf("busy@ was attempted %d times; nothing should be sent while the history is unreadable", n)
	}
	if row == nil || row.Status == entities.OutboxStatusFailed {
		t.Fatalf("row = %+v, want it kept for a later attempt", row)
	}
}
