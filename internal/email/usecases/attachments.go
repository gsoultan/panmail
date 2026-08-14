package usecases

import (
	"fmt"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// MaxAttachmentBytes is the total size of a message's attachments.
//
// 25 MiB is the number the receiving side of the internet settled on: Gmail,
// Outlook and most SMTP servers refuse a message above roughly that, so
// anything larger is not a large email, it is an email that will bounce.
//
// The transport limit in cmd/api is deliberately larger than this, because it
// has to cover the same bytes after protojson base64-encodes them — about a
// third more — plus the body and headers. This is the limit that produces a
// useful error; that one only stops a process from being filled up.
const MaxAttachmentBytes = 25 << 20

// MaxAttachments bounds the count as well as the total. A thousand empty
// attachments cost little to send and are built into a MIME part each.
const MaxAttachments = 100

// validateAttachments rejects a message too large to deliver.
//
// Doing this at admission rather than at send is the whole point. An accepted
// message is serialised into the outbox row and stays there: every worker that
// claims it loads the whole thing into memory, fails to send it, and schedules
// a retry so the next worker can do the same. One oversized message becomes a
// permanent cost paid on a schedule, and the caller never finds out, because by
// then the RPC has long since returned success.
func validateAttachments(req *panmailv1.SendEmailRequest) error {
	if len(req.Attachments) == 0 {
		return nil
	}
	if len(req.Attachments) > MaxAttachments {
		return fmt.Errorf("message has %d attachments; the limit is %d",
			len(req.Attachments), MaxAttachments)
	}

	var total int
	for _, a := range req.Attachments {
		if a == nil {
			continue
		}
		total += len(a.Content)
		// Checked inside the loop as well as after it: summing first means
		// adding up gigabytes of already-received attachments before saying
		// no, and on a 32-bit build the total could wrap on the way.
		if total > MaxAttachmentBytes {
			return fmt.Errorf(
				"attachments total more than %d MiB, which most mail servers reject; "+
					"send a link instead", MaxAttachmentBytes>>20)
		}
	}
	return nil
}
