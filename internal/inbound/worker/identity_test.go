package worker

import (
	"testing"
	"time"

	"github.com/gsoultan/gsmail"
)

// The poller re-reads the newest messages every tick, so an identifier has to
// be a property of the message rather than of the moment it was read. It was
// uuid.New(): a fresh value every pass, which stored one arriving email again
// every interval and re-fired the tenant's webhooks each time.

const (
	tenantA  = "11111111-1111-1111-1111-111111111111"
	tenantB  = "22222222-2222-2222-2222-222222222222"
	provider = "33333333-3333-3333-3333-333333333333"
)

func msg(headers map[string]string) gsmail.Email {
	return gsmail.Email{
		From: "sender@example.com", To: []string{"box@example.com"},
		Subject: "Hello", Body: []byte("body"), Headers: headers,
	}
}

func TestTheSameMessageReadTwiceGetsTheSameIdentity(t *testing.T) {
	e := msg(map[string]string{"Message-ID": "<abc@example.com>"})

	first := messageIdentity(tenantA, provider, e)
	second := messageIdentity(tenantA, provider, e)

	if first != second {
		t.Errorf("two reads produced %s and %s; the message would be stored twice", first, second)
	}
}

func TestDifferentMessagesGetDifferentIdentities(t *testing.T) {
	a := messageIdentity(tenantA, provider, msg(map[string]string{"Message-ID": "<a@example.com>"}))
	b := messageIdentity(tenantA, provider, msg(map[string]string{"Message-ID": "<b@example.com>"}))

	if a == b {
		t.Error("two distinct messages collided, so one would be silently dropped")
	}
}

// IMAP servers disagree about the capitalisation of Message-ID, and matching
// only one spelling would fall through to the weaker fallbacks.
func TestTheMessageIdHeaderIsFoundWhateverItsCase(t *testing.T) {
	want := messageIdentity(tenantA, provider, msg(map[string]string{"Message-ID": "<a@example.com>"}))

	for _, spelling := range []string{"message-id", "MESSAGE-ID", "Message-Id"} {
		got := messageIdentity(tenantA, provider, msg(map[string]string{spelling: "<a@example.com>"}))
		if got != want {
			t.Errorf("%q produced a different identity", spelling)
		}
	}
}

// A broadcast legitimately reaches two tenants with the same Message-ID; they
// must not overwrite each other's copy.
func TestTenantsDoNotShareAnIdentity(t *testing.T) {
	e := msg(map[string]string{"Message-ID": "<broadcast@example.com>"})

	if messageIdentity(tenantA, provider, e) == messageIdentity(tenantB, provider, e) {
		t.Error("two tenants receiving the same message collide")
	}
}

func TestUidIsUsedWhenThereIsNoMessageId(t *testing.T) {
	e := msg(nil)
	e.UID = 42
	e.Mailbox = "INBOX"

	first := messageIdentity(tenantA, provider, e)
	if first != messageIdentity(tenantA, provider, e) {
		t.Error("a UID-derived identity is not stable")
	}

	// UID is unique only within a mailbox, so two providers using the same
	// number must not collide.
	other := messageIdentity(tenantA, "44444444-4444-4444-4444-444444444444", e)
	if first == other {
		t.Error("the same UID on two providers collided")
	}
}

func TestADifferentUidIsADifferentMessage(t *testing.T) {
	a := msg(nil)
	a.UID, a.Mailbox = 1, "INBOX"
	b := msg(nil)
	b.UID, b.Mailbox = 2, "INBOX"

	if messageIdentity(tenantA, provider, a) == messageIdentity(tenantA, provider, b) {
		t.Error("two UIDs collided")
	}
}

// Weaker than the others — two genuinely identical messages collapse into one
// — but still the better failure, because the alternative is storing the same
// message forever.
func TestAMessageWithNeitherFallsBackToItsContent(t *testing.T) {
	e := msg(nil)

	if messageIdentity(tenantA, provider, e) != messageIdentity(tenantA, provider, e) {
		t.Error("a content-derived identity is not stable")
	}

	other := msg(nil)
	other.Subject = "Something else"
	if messageIdentity(tenantA, provider, e) == messageIdentity(tenantA, provider, other) {
		t.Error("two different messages collided on content")
	}
}

func TestTheIdentityIsAUsableID(t *testing.T) {
	id := messageIdentity(tenantA, provider, msg(map[string]string{"Message-ID": "<a@example.com>"}))
	// The rest of the system treats inbound ids as UUID strings.
	if len(id) != 36 {
		t.Errorf("identity %q is not a UUID string", id)
	}
}

// The timestamp used to be time.Now(), which made it a property of when the
// poller happened to run: a mailbox read after an outage showed a week of mail
// as arriving at once, in the wrong order.
func TestTheMessagesOwnDateIsPreferred(t *testing.T) {
	e := msg(map[string]string{"Date": "Tue, 10 Feb 2026 15:04:05 +0000"})

	got := receivedAt(e)
	want := time.Date(2026, 2, 10, 15, 4, 5, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("received at %v, want the Date header %v", got, want)
	}
}

func TestAnUnparseableDateFallsBackToNow(t *testing.T) {
	before := time.Now().Add(-time.Second)
	got := receivedAt(msg(map[string]string{"Date": "not a date"}))

	if got.Before(before) {
		t.Errorf("received at %v, want roughly now", got)
	}
}

func TestNoDateHeaderFallsBackToNow(t *testing.T) {
	before := time.Now().Add(-time.Second)
	if got := receivedAt(msg(nil)); got.Before(before) {
		t.Errorf("received at %v, want roughly now", got)
	}
}

// gsmail reports which property it identified the message by, and the three
// produce different kinds of value. Keying on the value alone would let a
// content digest collide with someone's Message-ID.
func TestTheIdentitySourceIsPartOfTheKey(t *testing.T) {
	byHeader := msg(map[string]string{"Message-ID": "<abc@example.com>"})

	// The same string arriving as a UID rather than a Message-ID.
	byUID := msg(nil)
	byUID.UID = 7
	byUID.Mailbox = "INBOX"

	if messageIdentity(tenantA, provider, byHeader) == messageIdentity(tenantA, provider, byUID) {
		t.Error("two different kinds of identity collided")
	}
}

// A message with nothing to identify it by is stored without deduplication
// rather than given an invented id: it cannot be recognised on the next pass,
// and pretending otherwise would suppress a later, different message.
func TestAMessageWithNothingToIdentifyItGetsNoID(t *testing.T) {
	if got := messageIdentity(tenantA, provider, gsmail.Email{}); got != "" {
		t.Errorf("identity = %q, want empty for a message with no distinguishing property", got)
	}
}

// A Message-ID identifies the message wherever it is found, so the same mail
// reaching a tenant through two of their accounts is one message.
func TestAMessageIdIsNotScopedByProvider(t *testing.T) {
	e := msg(map[string]string{"Message-ID": "<abc@example.com>"})

	a := messageIdentity(tenantA, provider, e)
	b := messageIdentity(tenantA, "99999999-9999-9999-9999-999999999999", e)
	if a != b {
		t.Error("the same message read from two providers was stored twice")
	}
}
