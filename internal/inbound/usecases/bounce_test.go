package usecases

import (
	"context"
	"runtime"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
)

// A bounce read from the inbox suppresses the address it names, permanently
// when it is a hard one. So the address has to be the one the report is about,
// and nothing else.

type recordedBounce struct {
	eventType panmailv1.EmailEventType
	recipient string
}

type recordingEvents struct {
	eventusecases.ProcessEventUsecase
	recorded []recordedBounce
}

func (r *recordingEvents) RecordEvent(_ context.Context, _, _, _ string, eventType panmailv1.EmailEventType, recipient, _, _ string, _ map[string]any) error {
	r.recorded = append(r.recorded, recordedBounce{eventType, recipient})
	return nil
}

// Postfix's report, as it arrives. The address line has a space after the
// semicolon, which is how every major MTA writes it and what the old parser
// cut on, leaving the recipient empty.
const postfixDSN = `This is the mail system at host mx.example.net.

I'm sorry to have to inform you that your message could not
be delivered to one or more recipients.

<gone@example.org>: host mx.example.org[192.0.2.1] said: 550 5.1.1
    <gone@example.org>: Recipient address rejected: User unknown

Reporting-MTA: dns; mx.example.net
X-Postfix-Queue-ID: 4F1C2A0123
Arrival-Date: Sat, 10 Oct 2026 09:12:01 +0000 (UTC)

Final-Recipient: rfc822; gone@example.org
Original-Recipient: rfc822;gone@example.org
Action: failed
Status: 5.1.1
Remote-MTA: dns; mx.example.org
Diagnostic-Code: smtp; 550 5.1.1 <gone@example.org>: Recipient address
    rejected: User unknown
`

func TestTheBouncedAddressIsReadFromTheReport(t *testing.T) {
	cases := []struct {
		name string
		body string
		html string
		want string
	}{
		{name: "postfix, space after the semicolon", body: postfixDSN, want: "gone@example.org"},
		{name: "no space after the semicolon", body: "Final-Recipient: rfc822;a@example.org\r\nAction: failed\r\n", want: "a@example.org"},
		{name: "lower-case field name", body: "final-recipient: RFC822; b@example.org\n", want: "b@example.org"},
		{name: "bracketed address", body: "Final-Recipient: rfc822; <c@example.org>\n", want: "c@example.org"},
		{name: "only Original-Recipient", body: "Original-Recipient: rfc822; d@example.org\n", want: "d@example.org"},
		{name: "in the HTML part", html: "<p>Final-Recipient: rfc822; e@example.org<br>Action: failed</p>", want: "e@example.org"},
		{name: "at the very end", body: "Final-Recipient: rfc822; f@example.org", want: "f@example.org"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := &recordingEvents{}
			u := NewInboundUsecase(newFakeRepo(), events, &fakeWebhooks{})

			e := inbound("bounce-1", "Delivery Status Notification (Failure)")
			e.From = "MAILER-DAEMON@mx.example.net"
			e.BodyText, e.BodyHtml = tc.body, tc.html
			if err := u.Process(context.Background(), e); err != nil {
				t.Fatalf("process: %v", err)
			}

			if len(events.recorded) != 1 {
				t.Fatalf("recorded %d bounces, want 1", len(events.recorded))
			}
			if got := events.recorded[0].recipient; got != tc.want {
				t.Errorf("recorded the bounce against %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAPostfixReportIsAHardBounce(t *testing.T) {
	events := &recordingEvents{}
	u := NewInboundUsecase(newFakeRepo(), events, &fakeWebhooks{})

	e := inbound("bounce-1", "Delivery Status Notification (Failure)")
	e.BodyText = postfixDSN
	u.Process(context.Background(), e)

	if len(events.recorded) != 1 || events.recorded[0].eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE {
		t.Errorf("recorded %+v, want one hard bounce", events.recorded)
	}
}

// The attack this closes: a message dressed as a bounce, naming no recipient,
// from the address the sender wants suppressed. Falling back to From
// suppressed it, permanently.
func TestABounceThatNamesNoRecipientIsNotPinnedOnTheSender(t *testing.T) {
	events := &recordingEvents{}
	u := NewInboundUsecase(newFakeRepo(), events, &fakeWebhooks{})

	e := inbound("forged-1", "Undeliverable: hello")
	e.From = "victim@example.com"
	e.BodyText = "Delivery failed: user unknown"
	if err := u.Process(context.Background(), e); err != nil {
		t.Fatalf("process: %v", err)
	}

	for _, r := range events.recorded {
		if strings.EqualFold(r.recipient, e.From) {
			t.Fatalf("recorded a %s against the sender %q", r.eventType, r.recipient)
		}
	}
	if len(events.recorded) != 0 {
		t.Errorf("recorded %+v for a report that names nobody", events.recorded)
	}
}

// Bounce detection used to lower-case the whole of text+HTML afresh for each
// phrase it looked for, so a large message with a bounce-like subject cost
// several times its own size in garbage: about 200 MiB for a 25 MiB body.
func TestBounceDetectionDoesNotCopyALargeBodyRepeatedly(t *testing.T) {
	const bodySize = 25 << 20
	body := postfixDSN + strings.Repeat("x", bodySize)

	u := NewInboundUsecase(newFakeRepo(), &recordingEvents{}, &fakeWebhooks{})
	e := inbound("big-bounce", "Delivery Status Notification (Failure)")
	e.BodyText = body

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if err := u.Process(context.Background(), e); err != nil {
		t.Fatalf("process: %v", err)
	}
	runtime.ReadMemStats(&after)

	// Well under one copy of the body. Anything proportional to the body is
	// the regression.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > bodySize/4 {
		t.Errorf("processing a %d MiB bounce allocated %d MiB", bodySize>>20, allocated>>20)
	}
}
