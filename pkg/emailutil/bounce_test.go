package emailutil

import (
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"testing"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		errStr   string
		expected panmailv1.EmailEventType
	}{
		{
			name:     "Hard Bounce - User Unknown",
			errStr:   "550 5.1.1 The email account that you tried to reach does not exist.",
			expected: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
		},
		{
			name:     "Soft Bounce - Mailbox Full",
			errStr:   "452 4.2.2 The email account that you tried to reach is over quota.",
			expected: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
		},
		{
			name:     "Soft Bounce - Temporary Failure",
			errStr:   "421 4.3.0 Temporary system problem.",
			expected: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
		},
		{
			name:     "Spam Report",
			errStr:   "554 5.7.1 Message rejected due to spam content.",
			expected: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT,
		},
		{
			name:     "Unsubscribed",
			errStr:   "User has unsubscribed from your list.",
			expected: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED,
		},
		{
			name:     "Rejected",
			errStr:   "Connection rejected by the server.",
			expected: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED,
		},
		{
			name:     "Generic Bounce",
			errStr:   "Delivery failed for unknown reasons.",
			expected: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(tc.errStr)
			if got != tc.expected {
				t.Errorf("%s: ClassifyError(%s) = %s; want %s", tc.name, tc.errStr, got, tc.expected)
			}
		})
	}
}
