// Package retention owns how long panmail keeps each class of data it stores,
// and the single worker that enforces it.
//
// Retention is expressed in whole days and zero always means keep forever.
// Every class panmail persists appears in Policy: a store that is missing from
// it is a store that grows without bound, which is the condition this package
// exists to prevent.
package retention

import (
	"time"

	"github.com/gsoultan/panmail/internal/system_settings/entities"
)

const (
	// DefaultEventDays and DefaultWebhookDays are the two retentions panmail
	// enforced before any of this was configurable — a fortnight of delivery
	// events, a week of webhook notifications. They remain the defaults so
	// that upgrading changes nothing about what an existing deployment keeps.
	//
	// Every other class defaults to zero, which is a deliberate statement:
	// panmail will not start deleting message bodies, received mail, archives
	// or application logs because a version bump introduced a default nobody
	// agreed to.
	DefaultEventDays   = 14
	DefaultWebhookDays = 7

	// MaxDays bounds every field. Ten years is longer than any retention worth
	// expressing in days, and the bound keeps a hostile int32 from the wire out
	// of date arithmetic that would overflow into a cutoff in the future —
	// which would delete everything rather than nothing.
	MaxDays = 3650
)

// Policy is the resolved retention for every class of data panmail stores, in
// whole days, where zero means keep forever.
type Policy struct {
	EventDays   int
	MessageDays int
	OutboxDays  int
	WebhookDays int
	AppLogDays  int
	InboundDays int
	ArchiveDays int

	// QuarantineDays is how long a held message waits for a reviewer.
	QuarantineDays int
}

// Resolve reads the effective policy out of stored settings, applying the
// defaults for the two fields that have one and clamping everything into range.
//
// s is nil before anything has been stored, which is a first run rather than an
// error: the defaults are the answer.
func Resolve(s *entities.Settings) Policy {
	p := Policy{
		EventDays:   DefaultEventDays,
		WebhookDays: DefaultWebhookDays,
	}
	if s == nil {
		return p
	}

	// Absent means "use the default"; an explicit zero means forever. The
	// pointer is what distinguishes them — see entities.Settings.
	if s.LogRetentionDays != nil {
		p.EventDays = clampDays(*s.LogRetentionDays)
	}
	if s.WebhookRetentionDays != nil {
		p.WebhookDays = clampDays(*s.WebhookRetentionDays)
	}

	p.MessageDays = clampDays(s.MessageRetentionDays)
	p.OutboxDays = clampDays(s.OutboxRetentionDays)
	p.AppLogDays = clampDays(s.AppLogRetentionDays)
	p.InboundDays = clampDays(s.InboundRetentionDays)
	p.ArchiveDays = clampDays(s.ArchiveRetentionDays)
	p.QuarantineDays = clampDays(s.QuarantineRetentionDays)
	return p
}

// Apply writes the policy into stored settings in the encoding
// entities.Settings documents: the two fields with a non-zero default are
// stored as pointers so an administrator who deliberately chose "keep forever"
// survives a round trip and does not silently get the default back.
//
// Every field, and a test that populates every field. The version of this that
// wrote to the config file omitted QuarantineDays, so the quarantine retention
// an administrator set on the settings page was read, clamped and then dropped
// on the floor — the page showed zero again on the next load. The round-trip
// test missed it by leaving that field at zero in its fixture, which is the
// one value the bug could not distort.
func (p Policy) Apply(s *entities.Settings) {
	if s == nil {
		return
	}

	eventDays := clampDays(p.EventDays)
	webhookDays := clampDays(p.WebhookDays)
	s.LogRetentionDays = &eventDays
	s.WebhookRetentionDays = &webhookDays

	s.MessageRetentionDays = clampDays(p.MessageDays)
	s.OutboxRetentionDays = clampDays(p.OutboxDays)
	s.AppLogRetentionDays = clampDays(p.AppLogDays)
	s.InboundRetentionDays = clampDays(p.InboundDays)
	s.ArchiveRetentionDays = clampDays(p.ArchiveDays)
	s.QuarantineRetentionDays = clampDays(p.QuarantineDays)
}

// Duration converts a retention in days to the duration the outbox and webhook
// workers take, where zero keeps its meaning: pruning disabled.
func Duration(days int) time.Duration {
	if days <= 0 {
		return 0
	}
	return time.Duration(clampDays(days)) * 24 * time.Hour
}

// Cutoff returns the instant before which data is expired, and whether pruning
// is enabled at all. A caller that ignores the second return deletes
// everything, because the zero time is before all stored data.
func Cutoff(days int, now time.Time) (time.Time, bool) {
	if days <= 0 {
		return time.Time{}, false
	}
	return now.AddDate(0, 0, -clampDays(days)), true
}

// clampDays folds anything out of range back to a value that deletes no more
// than was asked for. A negative retention is nonsense rather than an
// instruction to delete everything, so it becomes "keep forever".
func clampDays(days int) int {
	if days < 0 {
		return 0
	}
	if days > MaxDays {
		return MaxDays
	}
	return days
}
