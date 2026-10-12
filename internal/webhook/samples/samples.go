// Package samples builds the example notification a subscriber receives for
// each webhook event.
//
// The data is made of the same types the real producers enqueue -- an
// EmailEvent for the delivery pipeline, the quarantine events for the filter,
// an InboundEmail for inbound mail -- and goes through the same envelope, so a
// sample cannot drift from the real shape without failing to compile or
// visibly changing here.
package samples

import (
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/emailfilter"
	evententities "github.com/gsoultan/panmail/internal/event/repositories/entities"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Fixed identifiers, so the samples read the same on every request and a
// subscriber can tell sample data from real data at a glance.
const (
	sampleMessageID = "00000000-0000-4000-8000-000000000001"
	sampleEventID   = "00000000-0000-4000-8000-000000000002"
	sampleReviewID  = "00000000-0000-4000-8000-000000000003"
	sampleInboundID = "00000000-0000-4000-8000-000000000004"
	sampleRuleID    = "00000000-0000-4000-8000-000000000005"
	sampleProvider  = "00000000-0000-4000-8000-000000000006"
)

// Events lists every event a subscription can listen for, in the order the
// enum declares them.
func Events() []panmailv1.WebhookTriggerEvent {
	var events []panmailv1.WebhookTriggerEvent
	for i := 1; ; i++ {
		e := panmailv1.WebhookTriggerEvent(i)
		if _, ok := panmailv1.WebhookTriggerEvent_name[int32(i)]; !ok {
			return events
		}
		events = append(events, e)
	}
}

// Known reports whether event is one a subscription can listen for.
func Known(event panmailv1.WebhookTriggerEvent) bool {
	_, ok := panmailv1.WebhookTriggerEvent_name[int32(event)]
	return ok && event != panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_UNSPECIFIED
}

// Description says in a sentence when the event is sent.
func Description(event panmailv1.WebhookTriggerEvent) string {
	switch event {
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT:
		return "A message was handed to a provider for delivery."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_DELIVERED:
		return "The recipient's mail server accepted the message."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_OPENED:
		return "The recipient opened the message (tracking pixel loaded)."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_CLICKED:
		return "The recipient clicked a tracked link; data.metadata.url is where it led."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED:
		return "The message bounced, hard or soft; data.type says which."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_REJECTED:
		return "A provider refused or dropped the message."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_INBOUND:
		return "An inbound message arrived (or was released from quarantine)."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_HELD:
		return "A filter rule quarantined a message and it is waiting for review."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_RELEASED:
		return "A reviewer released a held message."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_QUARANTINE_REJECTED:
		return "A reviewer refused a held message; it will not be sent or delivered."
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_EXPIRED:
		return "A held message reached its retention deadline with nobody having decided."
	}
	return ""
}

// Data returns the "data" object of a sample notification for event, or nil
// for an event that has none.
func Data(event panmailv1.WebhookTriggerEvent, tenantID string, now time.Time) any {
	now = now.UTC().Truncate(time.Second)
	switch event {
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT:
		return emailEvent(tenantID, now, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, nil, "")
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_DELIVERED:
		return emailEvent(tenantID, now, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, nil, "")
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_OPENED:
		return emailEvent(tenantID, now, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED, nil, "")
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_CLICKED:
		return emailEvent(tenantID, now, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED,
			map[string]any{"url": "https://example.com/pricing"}, "")
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED:
		return emailEvent(tenantID, now, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, nil,
			"550 5.1.1 The email account that you tried to reach does not exist")
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_REJECTED:
		return emailEvent(tenantID, now, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED, nil,
			"554 5.7.1 Message rejected due to content policy")
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_INBOUND:
		return &panmailv1.InboundEmail{
			Id:       sampleInboundID,
			TenantId: tenantID,
			From:     "customer@example.net",
			To:       []string{"support@example.com"},
			Subject:  "Question about my order",
			BodyText: "Hi, when will my order ship?",
			BodyHtml: "<p>Hi, when will my order ship?</p>",
			Headers: map[string]string{
				"Message-Id": "<sample-1@example.net>",
				"Date":       now.Format(time.RFC1123Z),
			},
			Timestamp: timestamppb.New(now),
		}
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_HELD:
		expires := now.Add(30 * 24 * time.Hour)
		return emailfilter.HeldEvent{
			ID:              sampleReviewID,
			Direction:       string(emailfilter.DirectionOutbound),
			MessageID:       sampleMessageID,
			RuleID:          sampleRuleID,
			RuleName:        "Hold large attachments",
			From:            "billing@example.com",
			Recipients:      []string{"jane@example.org"},
			Subject:         "Your invoice for October",
			SizeBytes:       12_582_912,
			AttachmentCount: 1,
			AttachmentNames: []string{"invoice-october.pdf"},
			Matched: []emailfilter.MatchedCondition{
				{Field: string(emailfilter.FieldTotalAttachmentSize), Operator: string(emailfilter.OpGreaterOrEqual), Number: 10_485_760},
			},
			HeldAt:    now,
			ExpiresAt: &expires,
		}
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_RELEASED:
		o := outcome(now)
		o.ReviewedBy = "reviewer@example.com"
		o.Note = "Expected invoice, safe to send"
		return o
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_QUARANTINE_REJECTED:
		o := outcome(now)
		o.ReviewedBy = "reviewer@example.com"
		o.Note = "Attachment not approved"
		return o
	case panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_EXPIRED:
		// No reviewer and no note: nobody decided, which is the point.
		return outcome(now)
	}
	return nil
}

func emailEvent(tenantID string, now time.Time, t panmailv1.EmailEventType, metadata map[string]any, errorMessage string) *evententities.EmailEvent {
	return &evententities.EmailEvent{
		ID:           sampleEventID,
		TenantID:     tenantID,
		ProviderID:   sampleProvider,
		ProviderName: "Primary SMTP",
		MessageID:    sampleMessageID,
		Type:         t,
		Recipient:    "jane@example.org",
		Subject:      "Welcome to Example",
		Timestamp:    now,
		Metadata:     metadata,
		ErrorMessage: errorMessage,
	}
}

func outcome(now time.Time) emailfilter.OutcomeEvent {
	return emailfilter.OutcomeEvent{
		ID:         sampleReviewID,
		Direction:  string(emailfilter.DirectionOutbound),
		MessageID:  sampleMessageID,
		RuleID:     sampleRuleID,
		RuleName:   "Hold large attachments",
		From:       "billing@example.com",
		Recipients: []string{"jane@example.org"},
		Subject:    "Your invoice for October",
		HeldAt:     now.Add(-2 * time.Hour),
		DecidedAt:  now,
	}
}
