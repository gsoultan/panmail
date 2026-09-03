package emailfilter

import (
	"context"
	"errors"
	"time"
)

// Status is where a filtered message is in its review.
type Status string

const (
	// StatusPending is waiting for someone to look at it.
	StatusPending Status = "PENDING"

	// StatusReleased means a reviewer let it through, and it was re-injected
	// into the pipeline it was taken out of.
	StatusReleased Status = "RELEASED"

	// StatusRejected means a reviewer refused it. Terminal.
	StatusRejected Status = "REJECTED"

	// StatusExpired means retention reached it before a reviewer did. Also
	// terminal, and deliberately distinct from rejected: nobody decided this,
	// the clock did, and a queue full of these means the review process is not
	// running rather than that the mail was bad.
	StatusExpired Status = "EXPIRED"
)

func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusReleased, StatusRejected, StatusExpired:
		return true
	}
	return false
}

// Terminal reports whether the status can still change.
func (s Status) Terminal() bool {
	return s == StatusReleased || s == StatusRejected || s == StatusExpired
}

// maxStoredSubject bounds what goes in the row. A subject header has no
// enforced length and a reviewer cannot read more than a line of it anyway;
// storing the whole of a pathological one would be a write nobody asked for.
const maxStoredSubject = 512

// FilteredMessage is one decision, recorded.
//
// It holds the envelope and the reason, which is what a reviewer triages on,
// and a reference to the payload rather than the payload itself. A held
// message carries whatever the sender attached, and a table that stores those
// bytes inline grows without bound at precisely the rate someone is abusing
// the system.
type FilteredMessage struct {
	ID        string
	TenantID  string
	Direction Direction

	// RuleID is nil-able and RuleName is not. Deleting a rule must not erase
	// the reason a message was held: "held by a rule that no longer exists" is
	// still an answer a reviewer can act on.
	RuleID   string
	RuleName string
	Action   Action

	Status Status

	// MessageID is the id the caller was given, outbound. Empty inbound.
	MessageID string

	From            string
	Recipients      []string
	Subject         string
	SizeBytes       int64
	AttachmentCount int
	AttachmentNames []string

	// Matched is the conditions that fired.
	Matched []Condition

	// PayloadRef locates the body and attachments. Empty when the payload was
	// not retained, which is the case for a reject: nothing will ever be
	// released, so keeping the bytes would be storing evidence nobody reads.
	PayloadRef string

	// ProviderID is carried outbound so a released message goes back out
	// through the provider it was originally addressed to, rather than
	// whichever one happens to be default at review time.
	ProviderID string

	ReviewedBy string
	ReviewedAt *time.Time
	ReviewNote string

	CreatedAt time.Time
	ExpiresAt *time.Time
}

// NewFilteredMessage records a decision against a message. Truncation happens
// here rather than at the store, so every caller gets the same bounds.
func NewFilteredMessage(tenantID string, direction Direction, m Message, d Decision) FilteredMessage {
	names := make([]string, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		names = append(names, a.Filename)
	}

	subject := m.Subject
	if len(subject) > maxStoredSubject {
		subject = subject[:maxStoredSubject]
	}

	record := FilteredMessage{
		TenantID:        tenantID,
		Direction:       direction,
		Action:          d.Action,
		Status:          StatusPending,
		From:            m.From,
		Recipients:      m.Recipients(),
		Subject:         subject,
		SizeBytes:       m.Size,
		AttachmentCount: len(m.Attachments),
		AttachmentNames: names,
		Matched:         d.Matched,
		ProviderID:      m.ProviderID,
	}
	if d.Rule != nil {
		record.RuleID = d.Rule.ID
		record.RuleName = d.Rule.Name
	}
	return record
}

// ErrNotFound is returned when a rule or a filtered message does not exist for
// the tenant asking. Not found and not yours are deliberately the same answer.
var ErrNotFound = errors.New("emailfilter: not found")

// ErrAlreadyReviewed reports a second decision on the same message. Releasing
// twice would send the mail twice, which is the one outcome a review queue
// must never produce.
var ErrAlreadyReviewed = errors.New("emailfilter: already reviewed")

// RuleRepository stores the rules a tenant configures.
type RuleRepository interface {
	Create(ctx context.Context, r *Rule) error
	Update(ctx context.Context, r *Rule) error
	Delete(ctx context.Context, tenantID, id string) error
	Get(ctx context.Context, tenantID, id string) (*Rule, error)

	// List returns every rule for a tenant, both directions, for the
	// management UI.
	List(ctx context.Context, tenantID string) ([]Rule, error)

	// Enabled returns the enabled rules for one direction in priority order,
	// validated and with patterns compiled. This is the send path's read.
	Enabled(ctx context.Context, tenantID string, direction Direction) ([]Rule, error)
}

// QuarantineFilter narrows a review queue listing.
type QuarantineFilter struct {
	Direction Direction
	Status    Status
	PageSize  int
	PageToken string
}

// QuarantineRepository stores decisions taken.
type QuarantineRepository interface {
	Create(ctx context.Context, m *FilteredMessage) error
	Get(ctx context.Context, tenantID, id string) (*FilteredMessage, error)
	List(ctx context.Context, tenantID string, f QuarantineFilter) ([]FilteredMessage, string, error)

	// Review moves a pending message to a terminal status. It must fail with
	// ErrAlreadyReviewed if the message is no longer pending, and that check
	// has to happen in the write itself rather than as a read followed by a
	// write — two reviewers clicking release at once is exactly the race that
	// sends a message twice.
	Review(ctx context.Context, tenantID, id string, status Status, reviewedBy, note string) (*FilteredMessage, error)

	// Expire moves everything past its expiry out of PENDING and returns the
	// records it moved — not a count, because each one is announced to the
	// tenant's webhook subscribers and a number cannot be. Retention calls it,
	// through the sweeper in expiry.go.
	//
	// Only rows this call transitioned. A message a reviewer decided between
	// the read and the write keeps their decision and must not appear here.
	Expire(ctx context.Context, now time.Time, limit int) ([]FilteredMessage, error)
}
