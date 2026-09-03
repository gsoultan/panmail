package postgres

import (
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/internal/storetest"
)

func newQuarantine(t *testing.T) emailfilter.QuarantineRepository {
	t.Helper()
	return NewQuarantineStore(storetest.NewConnection(t))
}

// held stores a pending message whose deadline is `in` from now. A negative
// duration is one already overdue.
func held(t *testing.T, q emailfilter.QuarantineRepository, id, tenant string, in time.Duration) *emailfilter.FilteredMessage {
	t.Helper()

	expires := time.Now().UTC().Add(in)
	record := &emailfilter.FilteredMessage{
		ID: storetest.ID(id), TenantID: tenant,
		Direction: emailfilter.DirectionOutbound,
		Action:    emailfilter.ActionHold, Status: emailfilter.StatusPending,
		// storetest.ID, not a readable string: message_id is a UUID column on
		// PostgreSQL and takes anything on SQLite, so a raw "m-overdue" here
		// passes one engine and fails the other.
		RuleName: "big attachments", MessageID: storetest.ID("m-" + id), PayloadRef: storetest.ID("m-" + id),
		From: "alice@example.com", Recipients: []string{"bob@partner.net"},
		Subject: "Quarterly numbers", CreatedAt: time.Now().UTC().Add(-time.Hour),
		ExpiresAt: &expires,
	}
	if err := q.Create(t.Context(), record); err != nil {
		t.Fatalf("Create %s: %v", id, err)
	}
	return record
}

// Expire returns the records rather than a count, because each one is
// announced to the tenant's subscribers and a number cannot be.
func TestExpireReturnsWhatItExpired(t *testing.T) {
	q := newQuarantine(t)
	held(t, q, "overdue", storetest.TenantA, -time.Hour)
	held(t, q, "not-yet", storetest.TenantA, time.Hour)

	expired, err := q.Expire(t.Context(), time.Now().UTC(), 500)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expired %d messages, want only the overdue one", len(expired))
	}
	if expired[0].ID != storetest.ID("overdue") {
		t.Errorf("expired %q, want the overdue message", expired[0].ID)
	}
	if expired[0].Status != emailfilter.StatusExpired {
		t.Errorf("status = %q, want EXPIRED", expired[0].Status)
	}
	// The subject and recipients have to survive the read, or an expiry
	// notification cannot stand on its own.
	if expired[0].Subject != "Quarterly numbers" || len(expired[0].Recipients) != 1 {
		t.Errorf("record came back thin: %+v", expired[0])
	}
}

// A second sweep must return nothing. Returning the same rows again would
// announce every expiry once per retention pass, for as long as the row lives.
func TestASecondSweepExpiresNothing(t *testing.T) {
	q := newQuarantine(t)
	held(t, q, "overdue", storetest.TenantA, -time.Hour)

	if _, err := q.Expire(t.Context(), time.Now().UTC(), 500); err != nil {
		t.Fatalf("first Expire: %v", err)
	}
	again, err := q.Expire(t.Context(), time.Now().UTC(), 500)
	if err != nil {
		t.Fatalf("second Expire: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("the second sweep expired %d messages again", len(again))
	}
}

// The race the status guard exists for. A reviewer who decides an overdue
// message keeps their decision, and it must not also be announced as an
// expiry — those are opposite facts about the same message.
func TestAMessageDecidedBeforeTheSweepIsNotExpired(t *testing.T) {
	q := newQuarantine(t)
	held(t, q, "overdue", storetest.TenantA, -time.Hour)

	if _, err := q.Review(t.Context(), storetest.TenantA, storetest.ID("overdue"),
		emailfilter.StatusReleased, "alice", "looks fine"); err != nil {
		t.Fatalf("Review: %v", err)
	}

	expired, err := q.Expire(t.Context(), time.Now().UTC(), 500)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("the sweep expired %d messages a reviewer had already decided", len(expired))
	}

	after, err := q.Get(t.Context(), storetest.TenantA, storetest.ID("overdue"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Status != emailfilter.StatusReleased {
		t.Errorf("status = %q, want the reviewer's RELEASED to stand", after.Status)
	}
}

// The sweep is bounded so a quarantine nobody works cannot hold a long write
// while the rest of retention waits behind it.
func TestExpireRespectsItsLimit(t *testing.T) {
	q := newQuarantine(t)
	for _, id := range []string{"a", "b", "c"} {
		held(t, q, id, storetest.TenantA, -time.Hour)
	}

	expired, err := q.Expire(t.Context(), time.Now().UTC(), 2)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if len(expired) != 2 {
		t.Errorf("expired %d messages with a limit of 2", len(expired))
	}
}
