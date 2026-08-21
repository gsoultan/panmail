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

	"github.com/gsoultan/panmail/internal/config"
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
}

// Resolve reads the effective policy out of cfg, applying the defaults for the
// two fields that have one and clamping everything into range.
//
// cfg is nil before setup has written a config file, which is a first run
// rather than an error: the defaults are the answer.
func Resolve(cfg *config.Config) Policy {
	p := Policy{
		EventDays:   DefaultEventDays,
		WebhookDays: DefaultWebhookDays,
	}
	if cfg == nil {
		return p
	}

	// Absent means "use the default"; an explicit zero means forever. The
	// pointer is what distinguishes them — see config.AppConfig.
	if cfg.App.LogRetentionDays != nil {
		p.EventDays = clampDays(*cfg.App.LogRetentionDays)
	}
	if cfg.App.WebhookRetentionDays != nil {
		p.WebhookDays = clampDays(*cfg.App.WebhookRetentionDays)
	}

	p.MessageDays = clampDays(cfg.App.MessageRetentionDays)
	p.OutboxDays = clampDays(cfg.App.OutboxRetentionDays)
	p.AppLogDays = clampDays(cfg.App.AppLogRetentionDays)
	p.InboundDays = clampDays(cfg.App.InboundRetentionDays)
	p.ArchiveDays = clampDays(cfg.App.ArchiveRetentionDays)
	return p
}

// Apply writes the policy into cfg in the encoding config.AppConfig documents:
// the two fields with a non-zero default are stored as pointers so an
// administrator who deliberately chose "keep forever" survives a round trip
// through the file and does not silently get the default back.
func (p Policy) Apply(cfg *config.Config) {
	if cfg == nil {
		return
	}

	eventDays := clampDays(p.EventDays)
	webhookDays := clampDays(p.WebhookDays)
	cfg.App.LogRetentionDays = &eventDays
	cfg.App.WebhookRetentionDays = &webhookDays

	cfg.App.MessageRetentionDays = clampDays(p.MessageDays)
	cfg.App.OutboxRetentionDays = clampDays(p.OutboxDays)
	cfg.App.AppLogRetentionDays = clampDays(p.AppLogDays)
	cfg.App.InboundRetentionDays = clampDays(p.InboundDays)
	cfg.App.ArchiveRetentionDays = clampDays(p.ArchiveDays)
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
