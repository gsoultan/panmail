package usecases

import (
	"fmt"
	"strings"

	"github.com/gsoultan/gsmail"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// The headers a reply carries so the recipient's client can thread it, and the
// read-receipt request. They are fields of the request rather than free-form
// headers because a caller-chosen header can override what the pipeline sets
// for itself -- the bounce reference, List-Unsubscribe -- and these are the
// ones a mail client actually needs.
const (
	headerInReplyTo                 = "In-Reply-To"
	headerReferences                = "References"
	headerDispositionNotificationTo = "Disposition-Notification-To"
)

const (
	// MaxMessageIDLength bounds one Message-ID. RFC 5322 sets no limit of its
	// own beyond the line length, and every real one is far shorter.
	MaxMessageIDLength = 255

	// MaxReferences bounds the References a request may carry. A long thread
	// has a long list, and it is trimmed to fit a header line when sent; this
	// only stops a request carrying an unbounded one.
	MaxReferences = 100

	// maxReferencesLineBytes keeps the References line inside the 998 octets
	// RFC 5322 allows, leaving room for the field name. gsmail does not fold
	// header lines, so the value has to fit on one.
	maxReferencesLineBytes = 900
)

// InvalidReplyHeaderError reports a Reply-To, In-Reply-To, References or
// read-receipt address that cannot be sent. The caller's to fix.
type InvalidReplyHeaderError struct {
	Field  string
	Reason string
}

func (e *InvalidReplyHeaderError) Error() string {
	return e.Field + " " + e.Reason
}

// validateReplyHeaders rejects reply and threading fields that could not be
// sent as they stand, before the message is queued.
func validateReplyHeaders(req *panmailv1.SendEmailRequest) error {
	if req.ReplyTo != "" {
		if _, err := gsmail.ParseEmailAddress(req.ReplyTo); err != nil {
			return &InvalidReplyHeaderError{Field: "reply_to", Reason: fmt.Sprintf("is not a valid address: %v", err)}
		}
	}
	if req.DispositionNotificationTo != "" {
		if _, err := gsmail.ParseEmailAddress(req.DispositionNotificationTo); err != nil {
			return &InvalidReplyHeaderError{Field: "disposition_notification_to", Reason: fmt.Sprintf("is not a valid address: %v", err)}
		}
	}
	if req.InReplyTo != "" {
		if reason := messageIDProblem(req.InReplyTo); reason != "" {
			return &InvalidReplyHeaderError{Field: "in_reply_to", Reason: reason}
		}
	}
	if len(req.References) > MaxReferences {
		return &InvalidReplyHeaderError{
			Field:  "references",
			Reason: fmt.Sprintf("has %d entries, the limit is %d", len(req.References), MaxReferences),
		}
	}
	for _, id := range req.References {
		if reason := messageIDProblem(id); reason != "" {
			return &InvalidReplyHeaderError{Field: "references", Reason: reason}
		}
	}
	return nil
}

// messageIDProblem says what is wrong with a Message-ID, or "" when nothing is.
//
// The check is the shape RFC 5322 gives a msg-id, <left@right>, in printable
// ASCII without spaces. It is not a full grammar: the point is that the value
// is one header token that cannot break the line it is written on.
func messageIDProblem(id string) string {
	if len(id) > MaxMessageIDLength {
		return fmt.Sprintf("Message-ID is %d characters long, the limit is %d", len(id), MaxMessageIDLength)
	}
	if len(id) < 5 || id[0] != '<' || id[len(id)-1] != '>' {
		return fmt.Sprintf("Message-ID %q must be in angle brackets, like <id@example.com>", id)
	}
	inner := id[1 : len(id)-1]
	for i := 0; i < len(inner); i++ {
		if c := inner[i]; c <= ' ' || c > '~' || c == '<' || c == '>' {
			return fmt.Sprintf("Message-ID %q may contain only printable ASCII without spaces", id)
		}
	}
	if at := strings.IndexByte(inner, '@'); at <= 0 || at == len(inner)-1 {
		return fmt.Sprintf("Message-ID %q must have the form <left@right>", id)
	}
	return ""
}

// applyReplyHeaders copies the reply, threading and read-receipt fields onto
// an outgoing message. The values were validated at admission.
func applyReplyHeaders(msg *gsmail.Email, req *panmailv1.SendEmailRequest) {
	msg.ReplyTo = req.ReplyTo

	set := func(name, value string) {
		if value == "" {
			return
		}
		if msg.Headers == nil {
			msg.Headers = make(map[string]string, 3)
		}
		msg.Headers[name] = value
	}
	set(headerInReplyTo, req.InReplyTo)
	set(headerReferences, referencesLine(req.References))
	set(headerDispositionNotificationTo, req.DispositionNotificationTo)
}

// referencesLine joins a thread's Message-IDs into one References value that
// fits a header line.
//
// When the whole list does not fit, it keeps the first ID and as many of the
// most recent as do, which is the trimming RFC 5322 section 3.6.4 describes:
// the root and the immediate parents are what a client threads on.
func referencesLine(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	if line := strings.Join(ids, " "); len(line) <= maxReferencesLineBytes {
		return line
	}

	size := len(ids[0])
	start := len(ids)
	for start > 1 && size+1+len(ids[start-1]) <= maxReferencesLineBytes {
		start--
		size += 1 + len(ids[start])
	}
	kept := append([]string{ids[0]}, ids[start:]...)
	return strings.Join(kept, " ")
}
