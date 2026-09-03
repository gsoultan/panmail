package emailfilter

import "time"

// QuarantineNotifier announces what happens to a quarantined message: that it
// was held, and then how the hold ended.
//
// Narrow on purpose: a verb per moment, no enum, no payload type from
// elsewhere. The generated webhook events live in the API package, and naming
// them here would make this package depend on the wire format of notifications
// it only describes. cmd/api adapts the durable webhook worker to this.
//
// A verb each rather than one method taking an outcome, for the same reason
// the wire has three events rather than one: the caller that releases a
// message and the sweep that expires one have nothing in common, and a shared
// method would have them pass a discriminator neither of them wants to hold.
//
// Fire-and-forget by design — nothing returns an error. The worker underneath
// is already durable and retrying, and a notification that could fail the
// decision would mean a message stayed held because a webhook table was busy.
type QuarantineNotifier interface {
	NotifyHeld(tenantID string, event HeldEvent)

	// NotifyReleased says a reviewer let a held message go.
	NotifyReleased(tenantID string, event OutcomeEvent)

	// NotifyRejected says a reviewer refused one for good.
	NotifyRejected(tenantID string, event OutcomeEvent)

	// NotifyExpired says nobody decided in time and the clock did.
	NotifyExpired(tenantID string, event OutcomeEvent)
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

// OutcomeEvent is what a subscriber receives when a hold ends.
//
// It repeats the identifying half of HeldEvent rather than referring back to
// it, so that an outcome stands on its own. A subscriber that only wants to
// know what expired unreviewed should not have to have stored every hold to
// make sense of it.
//
// Same privacy rule as HeldEvent, and for the same reason: no body, no
// attachment content. It carries no Matched either — why a message was held is
// the hold's story, and by the time it is released or refused the interesting
// fact is who decided and what they said.
type OutcomeEvent struct {
	// ID is the review-queue id, the same one the hold notification carried.
	ID string `json:"id"`

	Direction string `json:"direction"`

	// MessageID is what the sender was given, outbound. Empty inbound.
	MessageID string `json:"message_id,omitempty"`

	RuleID   string `json:"rule_id,omitempty"`
	RuleName string `json:"rule_name"`

	From       string   `json:"from"`
	Recipients []string `json:"recipients"`
	Subject    string   `json:"subject"`

	HeldAt time.Time `json:"held_at"`

	// DecidedAt is when the hold ended, however it ended.
	DecidedAt time.Time `json:"decided_at"`

	// ReviewedBy and Note are a person's, so both are empty on an expiry —
	// which is the point of it being a separate event. An expiry has no
	// reviewer, and reporting one as an empty string beside a release that has
	// a real one would invite a subscriber to treat the two the same.
	ReviewedBy string `json:"reviewed_by,omitempty"`
	Note       string `json:"note,omitempty"`
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

// outcomeEventFor builds the notification for a hold that has ended.
//
// decidedAt is passed rather than read off the record, because the expiry
// sweep stamps no ReviewedAt: nobody reviewed it. The sweep supplies its own
// clock, which is the same one that decided the message was overdue.
func outcomeEventFor(record *FilteredMessage, decidedAt time.Time) OutcomeEvent {
	return OutcomeEvent{
		ID:         record.ID,
		Direction:  string(record.Direction),
		MessageID:  record.MessageID,
		RuleID:     record.RuleID,
		RuleName:   record.RuleName,
		From:       record.From,
		Recipients: record.Recipients,
		Subject:    record.Subject,
		HeldAt:     record.CreatedAt,
		DecidedAt:  decidedAt,
		ReviewedBy: record.ReviewedBy,
		Note:       record.ReviewNote,
	}
}
