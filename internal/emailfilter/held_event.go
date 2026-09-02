package emailfilter

import "time"

// HeldNotifier announces that a message was quarantined.
//
// Narrow on purpose: one verb, no enum, no payload type from elsewhere. The
// generated webhook event lives in the API package, and naming it here would
// make this package depend on the wire format of a notification it only
// describes. cmd/api adapts the durable webhook worker to this.
//
// Fire-and-forget by design — it returns nothing. The worker it wraps is
// already durable and retrying, and a notification that could fail the hold
// would mean a message stayed unquarantined because a webhook table was busy.
type HeldNotifier interface {
	NotifyHeld(tenantID string, event HeldEvent)
}

// HeldEvent is what a subscriber receives when a rule quarantines a message.
//
// A purpose-built struct with explicit tags rather than the domain type, because
// this is a public contract. Marshalling FilteredMessage directly would put Go
// field names on the wire and make every later rename a breaking change nobody
// noticed making.
//
// It deliberately carries no message body and no attachment content — only what
// a subscriber needs to decide whether a human should look. The message itself
// is behind the review API, which is authenticated; a webhook endpoint is a URL
// a tenant typed once.
type HeldEvent struct {
	// ID is the review-queue id. Fetch the message with it, act on it with it.
	ID string `json:"id"`

	Direction string `json:"direction"`

	// MessageID is what the sender was given, outbound. Empty inbound.
	MessageID string `json:"message_id,omitempty"`

	RuleID   string `json:"rule_id,omitempty"`
	RuleName string `json:"rule_name"`

	From       string   `json:"from"`
	Recipients []string `json:"recipients"`
	Subject    string   `json:"subject"`

	SizeBytes       int64    `json:"size_bytes"`
	AttachmentCount int      `json:"attachment_count"`
	AttachmentNames []string `json:"attachment_names,omitempty"`

	// Matched is why, in the same words the review UI shows. A notification
	// saying only that something was held sends someone to the dashboard to
	// find out what; this lets them triage from the notification.
	Matched []MatchedCondition `json:"matched"`

	HeldAt time.Time `json:"held_at"`

	// ExpiresAt is when retention will give up on it. The point of sending it
	// is that a subscriber can escalate before then rather than discover
	// afterwards that a message expired unreviewed.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// MatchedCondition is one condition that fired, flattened for the wire.
type MatchedCondition struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
	Header   string   `json:"header,omitempty"`
	Number   int64    `json:"number,omitempty"`
}

// heldEventFor builds the notification for a recorded decision.
func heldEventFor(record *FilteredMessage) HeldEvent {
	matched := make([]MatchedCondition, 0, len(record.Matched))
	for _, c := range record.Matched {
		matched = append(matched, MatchedCondition{
			Field:    string(c.Field),
			Operator: string(c.Operator),
			Values:   c.Values,
			Header:   c.Header,
			Number:   c.Number,
		})
	}
	return HeldEvent{
		ID:              record.ID,
		Direction:       string(record.Direction),
		MessageID:       record.MessageID,
		RuleID:          record.RuleID,
		RuleName:        record.RuleName,
		From:            record.From,
		Recipients:      record.Recipients,
		Subject:         record.Subject,
		SizeBytes:       record.SizeBytes,
		AttachmentCount: record.AttachmentCount,
		AttachmentNames: record.AttachmentNames,
		Matched:         matched,
		HeldAt:          record.CreatedAt,
		ExpiresAt:       record.ExpiresAt,
	}
}
