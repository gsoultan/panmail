package usecases

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// A bounce read from the inbox suppresses the address it names, permanently
// when it is a hard one, and the inbox accepts mail from anyone. So a report
// is believed only when it is a real delivery status notification and it hands
// back the reference panmail signed onto the copy it reports on.

const (
	otherTenant = "22222222-2222-2222-2222-222222222222"
	sentMessage = "9b2f6c1e-0000-4000-8000-000000000001"
	gone        = "gone@example.org"
	victim      = "victim@example.org"
)

type recordedBounce struct {
	eventType panmailv1.EmailEventType
	messageID string
	recipient string
}

// recordingEvents records bounces and answers ListByMessageID from what it was
// told was sent, keyed by tenant and message as the real store is.
type recordingEvents struct {
	eventusecases.ProcessEventUsecase
	sent     map[string][]*panmailv1.EmailEvent
	recorded []recordedBounce
}

func newRecordingEvents() *recordingEvents {
	return &recordingEvents{sent: map[string][]*panmailv1.EmailEvent{}}
}

func (r *recordingEvents) markSent(tenantID, messageID, recipient string) {
	r.sent[tenantID+"/"+messageID] = append(r.sent[tenantID+"/"+messageID], &panmailv1.EmailEvent{
		TenantId: tenantID, MessageId: messageID, Recipient: recipient,
		Type: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
	})
}

func (r *recordingEvents) ListByMessageID(_ context.Context, tenantID, messageID string) ([]*panmailv1.EmailEvent, error) {
	return r.sent[tenantID+"/"+messageID], nil
}

func (r *recordingEvents) RecordEvent(_ context.Context, _, _, messageID string, eventType panmailv1.EmailEventType, recipient, _, _ string, _ map[string]any) error {
	r.recorded = append(r.recorded, recordedBounce{eventType, messageID, recipient})
	return nil
}

var testSigner = tracking.NewSigner([]byte("bounce-test-key"))

func newBounceUsecase(events *recordingEvents) InboundUsecase {
	u := NewInboundUsecase(newFakeRepo(), events, &fakeWebhooks{})
	return u.(*inboundUsecase).WithBounceSigner(testSigner)
}

// dsn renders a multipart/report the way Postfix does, reporting on
// finalRecipient and returning the original headers with ref in them ("" for
// none).
func dsn(finalRecipient, action, status, ref string) string {
	returned := "From: news@sender.example\r\nTo: " + gone + "\r\nSubject: Hello\r\nMessage-ID: <abc@sender.example>\r\n"
	if ref != "" {
		returned += tracking.BounceRefHeader + ": " + ref + "\r\n"
	}
	return "From: MAILER-DAEMON@mx.example.net\r\n" +
		"To: bounces@sender.example\r\n" +
		"Subject: Undelivered Mail Returned to Sender\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/report; report-type=delivery-status; boundary=\"B\"\r\n" +
		"\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\n" +
		"<" + finalRecipient + ">: host mx.example.org said: 550 5.1.1 User unknown\r\n" +
		"--B\r\nContent-Type: message/delivery-status\r\n\r\n" +
		"Reporting-MTA: dns; mx.example.net\r\n" +
		"Arrival-Date: Sat, 10 Oct 2026 09:12:01 +0000\r\n" +
		"\r\n" +
		"Final-Recipient: rfc822; " + finalRecipient + "\r\n" +
		"Original-Recipient: rfc822;" + finalRecipient + "\r\n" +
		"Action: " + action + "\r\n" +
		"Status: " + status + "\r\n" +
		"Diagnostic-Code: smtp; 550 5.1.1 <" + finalRecipient + ">: Recipient address rejected: User unknown\r\n" +
		"\r\n" +
		"--B\r\nContent-Type: text/rfc822-headers\r\n\r\n" +
		returned +
		"--B--\r\n"
}

// receive parses raw as the IMAP and POP3 receivers do and hands it to the
// usecase the way the poller does.
func receive(t *testing.T, u InboundUsecase, tenantID, raw string) {
	t.Helper()
	parsed, err := gsmail.ParseRawEmail([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	e := &panmailv1.InboundEmail{
		Id: fmt.Sprintf("in-%x", sha256.Sum256([]byte(raw))), TenantId: tenantID,
		From: parsed.From, To: parsed.To, Subject: parsed.Subject,
		BodyText: string(parsed.Body), BodyHtml: string(parsed.HTMLBody),
		Headers: parsed.Headers,
	}
	if err := u.Process(context.Background(), e, ParseDeliveryReport(parsed.Attachments)); err != nil {
		t.Fatalf("process: %v", err)
	}
}

func TestAGenuineReportForAMessagePanmailSentIsRecorded(t *testing.T) {
	events := newRecordingEvents()
	events.markSent(dedupTenant, sentMessage, gone)
	u := newBounceUsecase(events)

	receive(t, u, dedupTenant, dsn(gone, "failed", "5.1.1", testSigner.BounceRef(dedupTenant, sentMessage, gone)))

	want := recordedBounce{panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, sentMessage, gone}
	if len(events.recorded) != 1 || events.recorded[0] != want {
		t.Fatalf("recorded %+v, want exactly %+v", events.recorded, want)
	}
}

// The send path may record a recipient in its display-name form; the report
// names the bare address.
func TestAReportMatchesARecipientSentToByDisplayName(t *testing.T) {
	events := newRecordingEvents()
	events.markSent(dedupTenant, sentMessage, "gone person <"+gone+">")
	u := newBounceUsecase(events)

	receive(t, u, dedupTenant, dsn(gone, "failed", "5.1.1", testSigner.BounceRef(dedupTenant, sentMessage, gone)))

	if len(events.recorded) != 1 {
		t.Fatalf("recorded %+v, want one bounce", events.recorded)
	}
}

func TestADelayReportIsASoftBounceAndADeliveryReportIsNothing(t *testing.T) {
	events := newRecordingEvents()
	events.markSent(dedupTenant, sentMessage, gone)
	u := newBounceUsecase(events)
	ref := testSigner.BounceRef(dedupTenant, sentMessage, gone)

	receive(t, u, dedupTenant, dsn(gone, "delayed", "4.2.2", ref))
	receive(t, u, dedupTenant, dsn(gone, "delivered", "2.0.0", ref))

	if len(events.recorded) != 1 || events.recorded[0].eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE {
		t.Errorf("recorded %+v, want one soft bounce", events.recorded)
	}
}

// Each of these is something anyone who can mail the tenant's mailbox can
// send. None of them may record anything against anyone.
func TestForgedBounceReportsSuppressNoOne(t *testing.T) {
	victimRef := testSigner.BounceRef(dedupTenant, sentMessage, victim)

	cases := []struct {
		name   string
		tenant string
		raw    string
	}{
		{
			// What the old detector acted on: the right subject, the right
			// words, a Final-Recipient line. No report structure at all.
			name:   "plain-text user unknown naming the victim",
			tenant: dedupTenant,
			raw: "From: MAILER-DAEMON@mx.example.net\r\nSubject: Delivery Status Notification (Failure)\r\n\r\n" +
				"Delivery failed: user unknown\r\nFinal-Recipient: rfc822; " + victim + "\r\nAction: failed\r\nStatus: 5.1.1\r\n",
		},
		{
			name:   "a real report structure that returns no reference",
			tenant: dedupTenant,
			raw:    dsn(victim, "failed", "5.1.1", ""),
		},
		{
			// The forger received a copy of the message themselves and quotes
			// their own reference, naming someone else.
			name:   "naming an address the reference was not minted for",
			tenant: dedupTenant,
			raw:    dsn(victim, "failed", "5.1.1", testSigner.BounceRef(dedupTenant, sentMessage, gone)),
		},
		{
			// A genuine reference, but nothing in the event store says the
			// message ever went to this address.
			name:   "naming an address panmail never mailed",
			tenant: dedupTenant,
			raw:    dsn(victim, "failed", "5.1.1", victimRef),
		},
		{
			name:   "for another tenant's message",
			tenant: dedupTenant,
			raw:    dsn(gone, "failed", "5.1.1", testSigner.BounceRef(otherTenant, sentMessage, gone)),
		},
		{
			name:   "with a reference made up by the forger",
			tenant: dedupTenant,
			raw:    dsn(gone, "failed", "5.1.1", "v1."+sentMessage+".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := newRecordingEvents()
			// gone@ was mailed in both tenants; victim@ never was.
			events.markSent(dedupTenant, sentMessage, gone)
			events.markSent(otherTenant, sentMessage, gone)
			u := newBounceUsecase(events)

			receive(t, u, tc.tenant, tc.raw)

			if len(events.recorded) != 0 {
				t.Errorf("recorded %+v for a forged report", events.recorded)
			}
		})
	}
}

// A forged report is still mail: it is stored, it is just not believed.
func TestARefusedReportIsStillStoredAsInboundMail(t *testing.T) {
	repo := newFakeRepo()
	u := NewInboundUsecase(repo, newRecordingEvents(), &fakeWebhooks{}).(*inboundUsecase).WithBounceSigner(testSigner)

	parsed, _ := gsmail.ParseRawEmail([]byte(dsn(victim, "failed", "5.1.1", "")))
	e := inbound("forged-1", parsed.Subject)
	if err := u.Process(context.Background(), e, ParseDeliveryReport(parsed.Attachments)); err != nil {
		t.Fatalf("process: %v", err)
	}
	if repo.writes != 1 {
		t.Errorf("stored %d messages, want 1", repo.writes)
	}
}

// Without the key there is nothing to verify a reference against, so nothing
// is believed -- rather than everything.
func TestWithoutTheTrackingKeyNoReportIsRecorded(t *testing.T) {
	events := newRecordingEvents()
	events.markSent(dedupTenant, sentMessage, gone)
	u := NewInboundUsecase(newFakeRepo(), events, &fakeWebhooks{}).(*inboundUsecase).WithBounceSigner(tracking.NewSigner(nil))

	receive(t, u, dedupTenant, dsn(gone, "failed", "5.1.1", testSigner.BounceRef(dedupTenant, sentMessage, gone)))

	if len(events.recorded) != 0 {
		t.Errorf("recorded %+v with no key to verify against", events.recorded)
	}
}

func TestTheReportedAddressIsReadFromTheStatusPart(t *testing.T) {
	cases := []struct {
		name, field, want string
	}{
		{"space after the semicolon", "Final-Recipient: rfc822; a@example.org", "a@example.org"},
		{"no space", "Final-Recipient: rfc822;b@example.org", "b@example.org"},
		{"upper case", "Final-Recipient: RFC822; C@Example.org", "c@example.org"},
		{"bracketed", "Final-Recipient: rfc822; <d@example.org>", "d@example.org"},
		{"only Original-Recipient", "Original-Recipient: rfc822; e@example.org", "e@example.org"},
		{"not an Internet address", "Final-Recipient: x400; /c=us/o=x/", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := ParseDeliveryReport([]gsmail.Attachment{{
				ContentType: "message/delivery-status",
				Data:        []byte("Reporting-MTA: dns; mx.example.net\r\n\r\n" + tc.field + "\r\nAction: failed\r\nStatus: 5.1.1\r\n"),
			}})
			got := ""
			if report != nil {
				got = report.Recipients[0].Address
			}
			if got != tc.want {
				t.Errorf("read %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAMessageWithoutADeliveryStatusPartIsNotAReport(t *testing.T) {
	report := ParseDeliveryReport([]gsmail.Attachment{
		{ContentType: "text/rfc822-headers", Data: []byte("X-Panmail-Ref: v1.x.y\r\n\r\n")},
		{ContentType: "application/pdf", Data: []byte(strings.Repeat("Final-Recipient: rfc822; v@example.org\r\n", 2))},
	})
	if report != nil {
		t.Errorf("read a report from %+v", report)
	}
}
