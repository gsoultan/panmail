package emailutil

import (
	"regexp"
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

// recipientHardBounceCodes are the enhanced status codes that say the address
// itself is bad. They are matched as whole codes, never as substrings: a
// substring match on "5.1.1" read the provider's IP in "dial tcp
// 185.1.1.20:587: connect: connection refused" as a dead mailbox and
// suppressed the recipient for good.
var recipientHardBounceCodes = enhancedCodePattern("5.1.1", "5.1.2", "5.1.3", "5.1.6", "5.1.10")

// recipientHardBouncePatterns describe a permanent problem with the address.
// Codes that indicate a protocol or policy problem (5.5.x sequence errors,
// relay denials, authentication failures) are deliberately absent: they are
// permanent, but they are not the recipient's fault.
var recipientHardBouncePatterns = []string{
	"user unknown", "no such user", "no such recipient",
	"mailbox not found", "mailbox unavailable", "invalid recipient",
	"recipient address rejected", "address rejected", "account disabled",
	"user doesn't exist", "user does not exist", "unknown recipient",
	"recipient not found", "does not exist",
}

// complaintPatterns describe a recipient who has said they do not want this
// mail. They are phrases rather than single words on purpose: a bare "spam" or
// "complaint" also appears in a content filter's refusal ("554 5.7.1 Message
// rejected as spam"), which is a verdict on the message rather than on the
// recipient, and reading it as a complaint suppressed the address permanently.
var complaintPatterns = []string{
	"feedback loop", "spam complaint", "abuse complaint", "complained",
	"marked as spam", "reported as spam",
}

// contentRejectionPatterns describe the receiving server refusing this
// message, usually by a content or policy filter. Sending the same message
// again gets the same answer, but it says nothing about the address, so it is
// recorded as REJECTED and suppresses no one.
var contentRejectionPatterns = []string{
	"spam", "unacceptable content", "policy violation", "content rejected",
	"message rejected", "virus", "malware", "phishing",
}

// unsubscribePatterns describe a recipient who has opted out. "unsubscribe"
// alone is not one of them: it is as likely to be part of a list footer or a
// mailbox name as a statement that someone left.
var unsubscribePatterns = []string{
	"unsubscribed", "opted out",
}

// transientCodes match any enhanced status code of class 4: the server itself
// says the condition is temporary, whatever its text goes on to say.
var transientCodes = regexp.MustCompile(`(?:^|[^\d.])4\.\d{1,3}\.\d{1,3}(?:[^\d.]|$)`)

// transientPatterns describe a problem that may clear on its own — whether the
// recipient's mailbox is full or the connection failed.
var transientPatterns = []string{
	"mailbox full", "quota exceeded", "storage full", "over quota",
	"connection timed out", "connection refused", "connection reset",
	"i/o timeout", "no route to host", "network is unreachable",
	"server busy", "try again later", "greylist", "greylisted",
	"rate limit", "too many messages", "too many connections",
	"temporary failure", "temporarily deferred", "deferred",
	"context deadline exceeded", "eof",
}

// infrastructureCodes are the enhanced status codes for a fault on our side of
// the exchange: authentication required, or credentials invalid or expired.
var infrastructureCodes = enhancedCodePattern("5.7.0", "5.7.8", "5.7.9")

// infrastructurePatterns describe a fault on our side of the exchange: the
// provider rejected us, not the recipient. These must never suppress anyone.
// A blocklisting is here too: it is a verdict on the sending IP or domain,
// and it clears once the sender is delisted.
var infrastructurePatterns = []string{
	"authentication failed", "authentication credentials", "auth failed",
	"invalid credentials", "username and password not accepted",
	"relay access denied", "relaying denied", "relay denied",
	"not authorized to send", "sender address rejected",
	"blacklisted", "blocklisted",
	"certificate", "x509", "tls", "ssl",
	"unable to look up host", "no such host", "dns",
	"connection closed by remote host",
}

// replyCode finds the basic SMTP reply code: three digits that start the
// reply, either at the start of the error or after the prefix a client puts
// in front of it ("smtp rcpt to <addr>: 550 ..."). It must be followed by a
// space, a hyphen (a multi-line reply) or the end, so neither a port
// ("10.0.0.5:587:") nor an IP octet is taken for one.
var replyCode = regexp.MustCompile(`(?:^|[\s:])([2-5]\d\d)(?:[ -]|$)`)

// emailAddress matches an address anywhere in an error. Addresses are removed
// before classifying: otherwise a recipient called complaints@ or
// unsubscribe-team@, or one at dnsimple.com, decided the verdict instead of
// what the server actually said.
var emailAddress = regexp.MustCompile(`[^\s<>()\[\]"',;:]+@[^\s<>()\[\]"',;:]+`)

// enhancedCodePattern matches any of codes as a whole enhanced status code,
// never as part of a longer number such as an IP address or another code.
func enhancedCodePattern(codes ...string) *regexp.Regexp {
	quoted := make([]string, len(codes))
	for i, c := range codes {
		quoted[i] = regexp.QuoteMeta(c)
	}
	return regexp.MustCompile(`(?:^|[^\d.])(?:` + strings.Join(quoted, "|") + `)(?:[^\d.]|$)`)
}

// ClassifyError categorises a delivery error.
//
// Only what the server said is read. The error panmail holds usually wraps the
// reply in a client prefix that names the recipient ("smtp rcpt to <addr>:
// 550 ..."), or is not a reply at all but a dial error carrying the provider's
// IP. Matching words and codes anywhere in that string let the address or the
// IP pick the verdict, and a HARD_BOUNCE or SPAM_REPORT suppresses for good.
// So addresses are removed, the reply is read from its basic code onward, and
// status codes are matched whole.
//
// The basic code decides permanence before any wording does. A 4xx reply is
// the server saying "not now", and nothing in its text ("Recipient address
// rejected: Greylisted", "Mailbox unavailable, try again later") makes it a
// permanent statement about the recipient. For the same reason transient
// wording is checked before anything that could blame the recipient.
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

	text := emailAddress.ReplaceAllString(strings.ToLower(errStr), "")

	// Our own fault first: these often also carry a 5xx code, and must not be
	// mistaken for a statement about the recipient. This reads the whole
	// error, prefix included, because a "tls:" or "dial tcp: lookup" prefix
	// is exactly where such faults show.
	if containsAny(text, infrastructurePatterns) || infrastructureCodes.MatchString(text) {
		return Classification{
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED,
			Retryable: true,
		}
	}

	// Everything after this could blame the recipient, so it reads only the
	// server's reply when there is one.
	reply, code := text, ""
	if m := replyCode.FindStringSubmatchIndex(text); m != nil {
		code = text[m[2]:m[3]]
		reply = text[m[2]:]
	}

	if strings.HasPrefix(code, "4") || transientCodes.MatchString(reply) || containsAny(reply, transientPatterns) {
		return Classification{
			Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			Retryable: true,
		}
	}

	if containsAny(reply, unsubscribePatterns) {
		return Classification{
			Type:             panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED,
			RecipientAtFault: true,
		}
	}

	if containsAny(reply, complaintPatterns) {
		return Classification{
			Type:             panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT,
			RecipientAtFault: true,
		}
	}

	if containsAny(reply, contentRejectionPatterns) {
		return Classification{
			Type: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED,
		}
	}

	if recipientHardBounceCodes.MatchString(reply) || containsAny(reply, recipientHardBouncePatterns) {
		return Classification{
			Type:             panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
			RecipientAtFault: true,
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
