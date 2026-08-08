package emailutil

import (
	"strings"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// Classification is what a delivery error tells us about a recipient.
type Classification struct {
	// Type is the event to record.
	Type panmailv1.EmailEventType

	// RecipientAtFault reports whether the error says something about this
	// address specifically, as opposed to the connection, the credentials or
	// the provider. Only a recipient-level fault justifies suppressing an
	// address; a bad SMTP password says nothing about who was being mailed.
	RecipientAtFault bool

	// Retryable reports whether another attempt could plausibly succeed.
	Retryable bool
}

// recipientHardBouncePatterns describe a permanent problem with the address.
// Codes that indicate a protocol or policy problem (5.5.x sequence errors,
// relay denials, authentication failures) are deliberately absent: they are
// permanent, but they are not the recipient's fault.
var recipientHardBouncePatterns = []string{
	"5.1.1", "5.1.2", "5.1.3", "5.1.6", "5.1.10",
	"user unknown", "no such user", "no such recipient",
	"mailbox not found", "mailbox unavailable", "invalid recipient",
	"recipient address rejected", "address rejected", "account disabled",
	"user doesn't exist", "user does not exist", "unknown recipient",
	"recipient not found", "does not exist",
}

// complaintPatterns describe a recipient who does not want this mail.
var complaintPatterns = []string{
	"spam", "complaint", "feedback loop", "unacceptable content",
	"blacklisted", "blocklisted", "policy violation",
}

var unsubscribePatterns = []string{
	"unsubscribed", "unsubscribe",
}

// transientPatterns describe a problem that may clear on its own — whether the
// recipient's mailbox is full or the connection failed.
var transientPatterns = []string{
	"4.2.2", "mailbox full", "quota exceeded", "storage full", "over quota",
	"4.4.1", "4.4.2", "4.4.3", "4.4.7", "4.3.0", "4.3.1", "4.3.2", "4.3.5", "4.2.1",
	"connection timed out", "connection refused", "connection reset",
	"i/o timeout", "no route to host", "network is unreachable",
	"server busy", "try again later", "greylist", "greylisted",
	"rate limit", "too many messages", "too many connections",
	"temporary failure", "temporarily deferred", "deferred",
	"context deadline exceeded", "eof",
}

// infrastructurePatterns describe a fault on our side of the exchange: the
// provider rejected us, not the recipient. These must never suppress anyone.
var infrastructurePatterns = []string{
	"authentication failed", "authentication credentials", "auth failed",
	"invalid credentials", "username and password not accepted",
	"5.7.0", "5.7.8", "5.7.9",
	"relay access denied", "relaying denied", "relay denied",
	"not authorized to send", "sender address rejected",
	"certificate", "x509", "tls", "ssl",
	"unable to look up host", "no such host", "dns",
	"connection closed by remote host",
}

// ClassifyError categorises a delivery error.
//
// An unrecognised error is reported as unknown-but-retryable rather than being
// guessed at. Guessing was previously done by looking for a leading "5", which
// meant an SMTP authentication failure ("535 5.7.8 ...") read as a permanent
// recipient bounce and suppressed everyone it was sent to.
func ClassifyError(errStr string) Classification {
	if errStr == "" {
		return Classification{
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED,
			Retryable: false,
		}
	}

	lowered := strings.ToLower(errStr)

	// Our own fault first: these often also carry a 5xx code, and must not be
	// mistaken for a statement about the recipient.
	if containsAny(lowered, infrastructurePatterns) {
		return Classification{
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED,
			Retryable: true,
		}
	}

	if containsAny(lowered, unsubscribePatterns) {
		return Classification{
			Type:             panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED,
			RecipientAtFault: true,
		}
	}

	if containsAny(lowered, complaintPatterns) {
		return Classification{
			Type:             panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT,
			RecipientAtFault: true,
		}
	}

	if containsAny(lowered, recipientHardBouncePatterns) {
		return Classification{
			Type:             panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
			RecipientAtFault: true,
		}
	}

	if containsAny(lowered, transientPatterns) {
		return Classification{
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			Retryable: true,
		}
	}

	// Unrecognised. Retry rather than permanently condemning the address.
	return Classification{
		Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED,
		Retryable: true,
	}
}

func containsAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
