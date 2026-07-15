package emailutil

import (
	"strings"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// ClassifyError categorizes an error message into a bounce type.
// Standard SMTP codes:
// 4xx: Persistent Transient Failure (Soft Bounce)
// 5xx: Permanent Failure (Hard Bounce)
func ClassifyError(errStr string) panmailv1.EmailEventType {
	if errStr == "" {
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED
	}

	errStr = strings.ToLower(errStr)

	// Unsubscribed check (though usually handled by webhook)
	if strings.Contains(errStr, "unsubscribed") || strings.Contains(errStr, "unsubscribe") {
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED
	}

	// Spam/Complaint Patterns
	spamPatterns := []string{
		"spam", "blacklisted", "denied", "blocked", "policy violation",
		"5.7.1", "unacceptable content", "complaint", "feedback loop",
	}
	for _, p := range spamPatterns {
		if strings.Contains(errStr, p) {
			return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT
		}
	}

	// Hard Bounce Patterns (Permanent Failures - 5xx)
	hardBouncePatterns := []string{
		"5.1.1", "511", "user unknown", "no such user", "mailbox not found",
		"invalid recipient", "recipient address rejected", "account disabled",
		"domain not found", "5.1.2", "5.1.3", "5.1.4", "5.1.6", "5.1.7", "5.1.8",
		"permanent failure", "5.2.1", "5.2.2", "5.3.1", "5.4.1", "5.4.4", "5.4.6",
		"5.5.0", "5.5.1", "5.5.2", "5.5.3", "5.5.4", "5.5.5", "5.5.6", "5.6.0",
	}
	for _, p := range hardBouncePatterns {
		if strings.Contains(errStr, p) {
			return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE
		}
	}

	// Soft Bounce Patterns (Temporary Failures - 4xx)
	softBouncePatterns := []string{
		"4.2.2", "422", "mailbox full", "quota exceeded", "storage full",
		"4.4.1", "441", "connection timed out", "connection refused",
		"4.4.2", "442", "server busy", "try again later", "4.3.2",
		"4.2.1", "greylist", "rate limit", "too many messages",
		"temporary failure", "4.3.0", "4.3.1", "4.3.5", "4.4.3", "4.4.7",
	}
	for _, p := range softBouncePatterns {
		if strings.Contains(errStr, p) {
			return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE
		}
	}

	// Rejected (Special case, often permanent but generic)
	if strings.Contains(errStr, "rejected") {
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED
	}

	// Fallback detection based on code prefix
	if strings.Contains(errStr, " 5") || strings.HasPrefix(errStr, "5") {
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE
	}
	if strings.Contains(errStr, " 4") || strings.HasPrefix(errStr, "4") {
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE
	}

	return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED
}
