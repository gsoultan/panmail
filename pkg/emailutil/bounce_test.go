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
			name:             "spam report",
			errStr:           "554 5.7.1 Message rejected due to spam content.",
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
