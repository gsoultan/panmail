package emailutil

import (
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name             string
		errStr           string
		expected         panmailv1.EmailEventType
		recipientAtFault bool
		retryable        bool
	}{
		{
			name:             "hard bounce - user unknown",
			errStr:           "550 5.1.1 The email account that you tried to reach does not exist.",
			expected:         panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
			recipientAtFault: true,
		},
		{
			name:      "soft bounce - mailbox full",
			errStr:    "452 4.2.2 The email account that you tried to reach is over quota.",
			expected:  panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			retryable: true,
		},
		{
			name:      "soft bounce - temporary failure",
			errStr:    "421 4.3.0 Temporary system problem.",
			expected:  panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			retryable: true,
		},
		{
			// A content filter refusing the message is not the recipient
			// complaining. This case used to expect SPAM_REPORT, which
			// suppressed the address for good over a verdict on the content.
			name:     "content filter rejection",
			errStr:   "554 5.7.1 Message rejected due to spam content.",
			expected: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED,
		},
		{
			name:             "spam complaint",
			errStr:           "550 5.7.1 Recipient complained: message marked as spam via feedback loop",
			expected:         panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT,
			recipientAtFault: true,
		},
		{
			name:             "unsubscribed",
			errStr:           "User has unsubscribed from your list.",
			expected:         panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED,
			recipientAtFault: true,
		},
		{
			name:      "unrecognised errors are retried, not condemned",
			errStr:    "Delivery failed for unknown reasons.",
			expected:  panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED,
			retryable: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(tc.errStr)
			if got.Type != tc.expected {
				t.Errorf("ClassifyError(%q).Type = %s; want %s", tc.errStr, got.Type, tc.expected)
			}
			if got.RecipientAtFault != tc.recipientAtFault {
				t.Errorf("ClassifyError(%q).RecipientAtFault = %v; want %v", tc.errStr, got.RecipientAtFault, tc.recipientAtFault)
			}
			if got.Retryable != tc.retryable {
				t.Errorf("ClassifyError(%q).Retryable = %v; want %v", tc.errStr, got.Retryable, tc.retryable)
			}
		})
	}
}

// A fault on our side of the exchange says nothing about who was being mailed.
// Treating these as recipient bounces is what let one wrong SMTP password
// permanently suppress every address a tenant tried to reach.
func TestOurOwnFaultsNeverBlameTheRecipient(t *testing.T) {
	ourFaults := []string{
		"535 5.7.8 Error: authentication failed",
		"535 5.7.8 Username and Password not accepted",
		"550 5.7.1 Relay access denied",
		"554 5.7.1 <sender@example.com>: Sender address rejected: not authorized to send",
		"x509: certificate signed by unknown authority",
		"dial tcp: lookup smtp.example.com: no such host",
		"tls: handshake failure",
	}

	for _, errStr := range ourFaults {
		t.Run(errStr, func(t *testing.T) {
			got := ClassifyError(errStr)
			if got.RecipientAtFault {
				t.Errorf("ClassifyError(%q) blamed the recipient", errStr)
			}
			if !got.Retryable {
				t.Errorf("ClassifyError(%q) was not retryable; a fixable misconfiguration should not drop mail", errStr)
			}
		})
	}
}

// Transient network problems must not read as permanent.
func TestTransientNetworkFaultsAreRetryable(t *testing.T) {
	transient := []string{
		"dial tcp 10.0.0.5:587: connect: connection refused",
		"read tcp 10.0.0.5:587: i/o timeout",
		"context deadline exceeded",
		"451 4.3.2 Service temporarily unavailable, try again later",
		"421 too many connections from your host",
	}

	for _, errStr := range transient {
		t.Run(errStr, func(t *testing.T) {
			got := ClassifyError(errStr)
			if got.RecipientAtFault {
				t.Errorf("ClassifyError(%q) blamed the recipient for a network fault", errStr)
			}
			if !got.Retryable {
				t.Errorf("ClassifyError(%q) should be retryable", errStr)
			}
		})
	}
}

// The old fallback classified anything starting with "5" as a permanent
// recipient bounce. Nothing may reach that conclusion by digit alone.
func TestNoBlameFromALeadingDigit(t *testing.T) {
	ambiguous := []string{
		"500 command not recognized",
		"502 command not implemented",
		"503 5.5.1 bad sequence of commands",
		"554 transaction failed",
	}

	for _, errStr := range ambiguous {
		t.Run(errStr, func(t *testing.T) {
			if got := ClassifyError(errStr); got.RecipientAtFault {
				t.Errorf("ClassifyError(%q) blamed the recipient on a protocol error", errStr)
			}
		})
	}
}

// Only the server's reply may decide a verdict. Each of these once matched a
// pattern somewhere else in the error -- the provider's IP, the recipient's
// own address, or a 4xx reply's wording -- and most suppressed a recipient
// permanently for it.
func TestClassifyErrorReadsOnlyTheServersReply(t *testing.T) {
	tests := []struct {
		name             string
		errStr           string
		expected         panmailv1.EmailEventType
		recipientAtFault bool
		retryable        bool
	}{
		{
			name:      "an IP containing 5.1.1 is not a dead mailbox",
			errStr:    "dial tcp 185.1.1.20:587: connect: connection refused",
			expected:  panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			retryable: true,
		},
		{
			name:      "an IP containing 5.1.2 is not a bad domain",
			errStr:    "dial tcp 45.1.2.9:25: connect: connection refused",
			expected:  panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			retryable: true,
		},
		{
			name:      "greylisting is temporary however it is worded",
			errStr:    "smtp rcpt to <user@example.com>: 450 4.2.0 <user@example.com>: Recipient address rejected: Greylisted, see http://postgrey.schweikert.ch/help/example.com.html",
			expected:  panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			retryable: true,
		},
		{
			name:      "a 4xx mailbox unavailable is temporary",
			errStr:    "450 4.2.1 Mailbox unavailable, try again later",
			expected:  panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			retryable: true,
		},
		{
			name:      "a recipient called complaints@ is not a complaint",
			errStr:    "smtp rcpt to complaints@acme.com: 421 4.7.0 try again later",
			expected:  panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			retryable: true,
		},
		{
			name:      "a recipient called unsubscribe-team@ has not unsubscribed",
			errStr:    "smtp rcpt to unsubscribe-team@acme.com: 451 4.3.0 Temporary local problem",
			expected:  panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
			retryable: true,
		},
		{
			name:     "a content filter's spam verdict is not the recipient's complaint",
			errStr:   "554 5.7.1 Message rejected as spam",
			expected: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED,
		},
		{
			name:             "a recipient at dnsimple.com is not a DNS fault",
			errStr:           "smtp rcpt to nobody@dnsimple.com: 550 5.1.1 user unknown",
			expected:         panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
			recipientAtFault: true,
		},
		{
			name:             "a hard bounce behind the client prefix is still one",
			errStr:           "smtp rcpt to <gone@example.com>: 550 5.1.1 <gone@example.com>: Recipient address rejected: User unknown in local recipient table",
			expected:         panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
			recipientAtFault: true,
		},
		{
			name:             "a multi-line reply is read from its code",
			errStr:           "550-5.1.1 The email account that you tried to reach does not exist.",
			expected:         panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
			recipientAtFault: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(tc.errStr)
			if got.Type != tc.expected {
				t.Errorf("ClassifyError(%q).Type = %s; want %s", tc.errStr, got.Type, tc.expected)
			}
			if got.RecipientAtFault != tc.recipientAtFault {
				t.Errorf("ClassifyError(%q).RecipientAtFault = %v; want %v", tc.errStr, got.RecipientAtFault, tc.recipientAtFault)
			}
			if got.Retryable != tc.retryable {
				t.Errorf("ClassifyError(%q).Retryable = %v; want %v", tc.errStr, got.Retryable, tc.retryable)
			}
		})
	}
}

// Whatever a 4xx reply goes on to say, it is the server saying "not now".
func TestA4xxReplyNeverCondemnsTheRecipient(t *testing.T) {
	replies := []string{
		"450 4.1.1 <user@example.com>: Recipient address rejected: User unknown",
		"452 5.1.1 user unknown",
		"421 spam complaint threshold reached",
		"451 user has unsubscribed",
	}

	for _, errStr := range replies {
		t.Run(errStr, func(t *testing.T) {
			got := ClassifyError(errStr)
			if got.RecipientAtFault || !got.Retryable {
				t.Errorf("ClassifyError(%q) = %+v; a 4xx reply is temporary", errStr, got)
			}
		})
	}
}
