package emailfilter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/emailfilter"
)

// stubExpiry is a quarantine whose Expire returns exactly what a real one
// would: the records this call transitioned, and nothing a reviewer decided
// first.
type stubExpiry struct {
	records []emailfilter.FilteredMessage
	err     error
	calls   int
}

func (s *stubExpiry) Create(context.Context, *emailfilter.FilteredMessage) error { return nil }
func (s *stubExpiry) Get(context.Context, string, string) (*emailfilter.FilteredMessage, error) {
	return nil, nil
}
func (s *stubExpiry) List(context.Context, string, emailfilter.QuarantineFilter) ([]emailfilter.FilteredMessage, string, error) {
	return nil, "", nil
}
func (s *stubExpiry) Review(context.Context, string, string, emailfilter.Status, string, string) (*emailfilter.FilteredMessage, error) {
	return nil, nil
}

func (s *stubExpiry) Expire(context.Context, time.Time, int) ([]emailfilter.FilteredMessage, error) {
	s.calls++
	return s.records, s.err
}

func expiredRecord(id, tenant string) emailfilter.FilteredMessage {
	return emailfilter.FilteredMessage{
		ID: id, TenantID: tenant, Direction: emailfilter.DirectionOutbound,
		Action: emailfilter.ActionHold, Status: emailfilter.StatusExpired,
		RuleName: "big attachments", MessageID: "m-" + id,
		From: "alice@example.com", Recipients: []string{"bob@partner.net"},
		Subject: "Quarterly numbers",
	}
}

// The event MAIL_HELD exists to prevent. A queue nobody works expires its
// messages on a timer, and before this that happened without a sound.
func TestExpirySweepAnnouncesEveryMessageItExpired(t *testing.T) {
	q := &stubExpiry{records: []emailfilter.FilteredMessage{
		expiredRecord("q1", "t1"),
		expiredRecord("q2", "t1"),
	}}
	n := &countingNotifier{}

	count, err := emailfilter.NewExpirySweeper(q, n).Expire(context.Background(), time.Now(), 500)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 — retention logs this number", count)
	}
	if len(n.expired) != 2 {
		t.Fatalf("announced %d expiries, want one per message", len(n.expired))
	}
	if len(n.released) != 0 || len(n.rejected) != 0 {
		t.Errorf("an expiry also announced %d releases and %d rejections", len(n.released), len(n.rejected))
	}

	// No reviewer, and that is the point of it being its own event rather than
	// a rejection with an empty name.
	if n.expired[0].ReviewedBy != "" || n.expired[0].Note != "" {
		t.Errorf("expiry carried a reviewer %q and note %q; nobody reviewed it",
			n.expired[0].ReviewedBy, n.expired[0].Note)
	}
	if n.expired[0].Subject != "Quarterly numbers" {
		t.Errorf("subject = %q; an expiry has to stand alone, without the hold event",
			n.expired[0].Subject)
	}
	if n.expired[0].DecidedAt.IsZero() {
		t.Error("decided_at is zero; the sweep's own clock decided this")
	}
}

// The store returns only rows it transitioned, so a message a reviewer decided
// between the read and the write never reaches the sweeper. This pins that the
// sweeper announces what it was given and does not go looking for more.
func TestExpirySweepAnnouncesNothingWhenNothingExpired(t *testing.T) {
	n := &countingNotifier{}

	count, err := emailfilter.NewExpirySweeper(&stubExpiry{}, n).Expire(context.Background(), time.Now(), 500)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if count != 0 || len(n.expired) != 0 {
		t.Errorf("count = %d with %d notifications, want a silent no-op", count, len(n.expired))
	}
}

// A failed sweep must not announce anything. Telling subscribers a message
// expired when the write failed would report a loss that did not happen.
func TestAFailedSweepAnnouncesNothing(t *testing.T) {
	q := &stubExpiry{records: []emailfilter.FilteredMessage{expiredRecord("q1", "t1")},
		err: errors.New("database unreachable")}
	n := &countingNotifier{}

	if _, err := emailfilter.NewExpirySweeper(q, n).Expire(context.Background(), time.Now(), 500); err == nil {
		t.Fatal("a failed sweep reported success")
	}
	if len(n.expired) != 0 {
		t.Errorf("a failed sweep announced %d expiries", len(n.expired))
	}
}

// Retention runs on every instance, so the sweeper must work with no notifier
// on a deployment that has no webhook worker.
func TestSweepingWithoutANotifierStillExpires(t *testing.T) {
	q := &stubExpiry{records: []emailfilter.FilteredMessage{expiredRecord("q1", "t1")}}

	count, err := emailfilter.NewExpirySweeper(q, nil).Expire(context.Background(), time.Now(), 500)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want the sweep to work without anyone listening", count)
	}
}
