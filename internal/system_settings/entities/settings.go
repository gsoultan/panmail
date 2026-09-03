// Package entities holds the global settings an administrator edits, as a
// domain type rather than as a file.
//
// These used to live only in config.yaml, written back by the settings page
// through config.Save. That works for one gateway and quietly stops working for
// two: the save reaches whichever instance served the request, and the others
// keep the old values with nothing to say they diverged. On Kubernetes it does
// not even reach one, because the config is a Secret volume and those are
// mounted read-only. The values are now in the database, which every instance
// already shares.
//
// The config file is still read once, to seed a database that has no row yet,
// so an existing deployment keeps what it configured. After that it is
// deployment configuration — database, keys, paths — and nothing edits it at
// runtime.
package entities

import (
	"time"

	"github.com/gsoultan/panmail/internal/config"
)

// Settings is the global configuration, as stored.
//
// Every retention is a whole number of days and zero means keep forever. The
// two pointers are the fields whose default is not zero, and the distinction is
// load bearing: absent resolves to 14 days for delivery events and 7 for
// webhook notifications, while an explicit zero from an administrator means
// forever. A plain int cannot tell those apart, and reading a chosen zero as
// "unset, use the default" is how a retention setting silently stops meaning
// what the settings page shows. The columns behind them are nullable for the
// same reason. Resolution and clamping live in internal/retention.
type Settings struct {
	BaseURL      string
	RetryPattern []string

	LogRetentionDays     *int
	WebhookRetentionDays *int

	MessageRetentionDays    int
	OutboxRetentionDays     int
	AppLogRetentionDays     int
	InboundRetentionDays    int
	ArchiveRetentionDays    int
	QuarantineRetentionDays int

	UpdatedAt time.Time
}

// FromConfig reads settings out of a loaded config file.
//
// This is the seed path and nothing else calls it. It is deliberately a plain
// copy: config.Load has already applied migrateRetention, which is what decides
// whether a `log_retention_days: 0` on disk is a chosen forever or the artefact
// of an older writer. Re-deciding it here would undo that.
//
// A nil config is a first run — no file yet — and gives zero values, which
// resolve to the defaults.
func FromConfig(cfg *config.Config) *Settings {
	s := &Settings{}
	if cfg == nil {
		return s
	}
	s.BaseURL = cfg.App.BaseURL
	s.RetryPattern = cfg.App.RetryPattern
	s.LogRetentionDays = cfg.App.LogRetentionDays
	s.WebhookRetentionDays = cfg.App.WebhookRetentionDays
	s.MessageRetentionDays = cfg.App.MessageRetentionDays
	s.OutboxRetentionDays = cfg.App.OutboxRetentionDays
	s.AppLogRetentionDays = cfg.App.AppLogRetentionDays
	s.InboundRetentionDays = cfg.App.InboundRetentionDays
	s.ArchiveRetentionDays = cfg.App.ArchiveRetentionDays
	s.QuarantineRetentionDays = cfg.App.QuarantineRetentionDays
	return s
}
