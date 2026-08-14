package usecases

import (
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

func attachment(n int) *panmailv1.Attachment {
	return &panmailv1.Attachment{Filename: "f.bin", Content: make([]byte, n)}
}

// An oversized message accepted here is not one bad request, it is a permanent
// one: it is serialised into the outbox row, and every worker that claims it
// loads the whole thing, fails, and schedules a retry so the next worker can
// do the same. Refusing it while the caller is still listening is the only
// point at which anyone finds out.
func TestAMessageTooLargeToDeliverIsRefused(t *testing.T) {
	req := &panmailv1.SendEmailRequest{
		Attachments: []*panmailv1.Attachment{attachment(MaxAttachmentBytes + 1)},
	}

	err := validateAttachments(req)
	if err == nil {
		t.Fatal("a message larger than any mail server will accept was queued")
	}
	// The error has to be actionable: "invalid request" leaves the caller
	// resending it.
	if !strings.Contains(err.Error(), "25") {
		t.Errorf("error %q does not say what the limit is", err)
	}
}

func TestAMessageAtTheLimitIsAccepted(t *testing.T) {
	req := &panmailv1.SendEmailRequest{
		Attachments: []*panmailv1.Attachment{attachment(MaxAttachmentBytes)},
	}
	if err := validateAttachments(req); err != nil {
		t.Errorf("a message exactly at the limit was refused: %v", err)
	}
}

// The limit is on the message, not on any one attachment. Splitting the same
// payload across several parts must not get around it.
func TestTheLimitIsOnTheWholeMessage(t *testing.T) {
	var parts []*panmailv1.Attachment
	for range 6 {
		parts = append(parts, attachment(5<<20)) // 30 MiB in total
	}

	if err := validateAttachments(&panmailv1.SendEmailRequest{Attachments: parts}); err == nil {
		t.Error("six 5 MiB attachments passed a 25 MiB limit")
	}
}

// Size is not the only cost. Each attachment becomes a MIME part, and empty
// ones are free to send.
func TestTooManyAttachmentsAreRefusedEvenWhenSmall(t *testing.T) {
	var parts []*panmailv1.Attachment
	for range MaxAttachments + 1 {
		parts = append(parts, attachment(0))
	}

	err := validateAttachments(&panmailv1.SendEmailRequest{Attachments: parts})
	if err == nil {
		t.Fatal("a message with more attachments than the limit was accepted")
	}
	if !strings.Contains(err.Error(), "attachments") {
		t.Errorf("error %q does not name the problem", err)
	}
}

func TestAMessageWithNoAttachmentsIsFine(t *testing.T) {
	if err := validateAttachments(&panmailv1.SendEmailRequest{}); err != nil {
		t.Errorf("a message with no attachments was refused: %v", err)
	}
}

// protojson can produce a nil entry, and a validator that panics on one is a
// worse failure than the one it was added to prevent.
func TestANilAttachmentDoesNotPanic(t *testing.T) {
	req := &panmailv1.SendEmailRequest{
		Attachments: []*panmailv1.Attachment{nil, attachment(10), nil},
	}
	if err := validateAttachments(req); err != nil {
		t.Errorf("nil entries were treated as an error: %v", err)
	}
}
