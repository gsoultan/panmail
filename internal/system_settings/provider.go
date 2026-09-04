// Package systemsettings publishes the stored settings to everything that
// reads them on a hot path.
package systemsettings

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/gsoultan/panmail/internal/redact"
	"github.com/gsoultan/panmail/internal/system_settings/entities"
	"github.com/gsoultan/panmail/internal/system_settings/repositories"
)

// DefaultRefreshInterval is how often a gateway re-reads the settings row.
//
// The point of the interval is cross-instance convergence: the instance that
// served the save has the new values immediately, and every other instance
// picks them up within one tick. A single-row primary-key SELECT once a minute
// per instance costs nothing next to that.
const DefaultRefreshInterval = time.Minute

// Provider holds the settings currently in force and refreshes them on a
// timer.
//
// Reads are from an atomic pointer rather than from the database, because the
// two busiest callers are on the send path: every tracking and unsubscribe link
// needs the base URL, and every deferral needs the retry pattern. A read there
// that touched the database would put a query behind each link in each message,
// which is the shape of regression the send path was measured to remove.
//
// The trade is that a change takes up to one interval to reach an instance that
// did not serve the save. That is deliberate and is the same bargain the
// retention policy already makes.
type Provider struct {
	repo     repositories.SettingsRepository
	interval time.Duration

	// nil until the first successful load. Readers treat that as "nothing
	// stored", which is what a first run is.
	current atomic.Pointer[entities.Settings]
}

func NewProvider(repo repositories.SettingsRepository, interval time.Duration) *Provider {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	return &Provider{repo: repo, interval: interval}
}

// Refresh reads the row and publishes it. Callers that have just written should
// call it so their own next read is not a tick behind.
func (p *Provider) Refresh(ctx context.Context) error {
	s, err := p.repo.Get(ctx)
	if err != nil {
		// Keep serving the last good values. Reverting to defaults because one
		// query failed would apply a base URL and a retry pattern nobody chose.
		return err
	}
	p.current.Store(s)
	return nil
}

// Get returns the settings in force, and satisfies retention.SettingsSource.
//
// It never returns an error: the caller wants the current policy, and the
// refresh loop is what decides how current that is.
func (p *Provider) Get(context.Context) (*entities.Settings, error) {
	return p.current.Load(), nil
}

// Start refreshes until ctx ends. The first read happens before the first tick,
// so a caller that starts the provider and then reads gets stored values rather
// than defaults.
func (p *Provider) Start(ctx context.Context) {
	if err := p.Refresh(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("could not read system settings; using defaults until the next refresh", "error", err)
	}

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.Refresh(ctx); err != nil && ctx.Err() == nil {
				// Warn rather than error: the previous values are still in
				// force, so this is degraded, not broken.
				slog.Warn("could not refresh system settings; keeping the last known values", "error", err)
			}
		}
	}
}

// BaseURL is the configured public URL with any trailing slash removed, or ""
// when none is set. Empty disables tracking and unsubscribe links rather than
// producing relative ones, which is what the send usecase checks for.
func (p *Provider) BaseURL() string {
	s := p.current.Load()
	if s == nil {
		return ""
	}
	return trimTrailingSlashes(s.BaseURL)
}

// RetryPattern is the deployment-wide backoff schedule, or nil when none is
// set. A tenant's own pattern still takes precedence over this.
func (p *Provider) RetryPattern() []string {
	s := p.current.Load()
	if s == nil || len(s.RetryPattern) == 0 {
		return nil
	}
	return s.RetryPattern
}

// ContentRedaction is the redaction level in force.
//
// It resolves through redact.LevelFromString, so an unset column and an
// unrecognised one both give the safe default rather than Off. That matters on
// a rolled-back deploy: a row written by a newer build naming a level this one
// does not know must not read as "show everything".
func (p *Provider) ContentRedaction() redact.Level {
	s := p.current.Load()
	if s == nil {
		return redact.Passwords
	}
	return redact.LevelFromString(s.ContentRedaction)
}

// trimTrailingSlashes loops rather than using a regexp anchored at one end.
// The SDKs learned this the expensive way: `/\/+$/` on a URL that is mostly
// slashes backtracks from every one of them.
func trimTrailingSlashes(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
