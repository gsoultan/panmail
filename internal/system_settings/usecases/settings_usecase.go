package usecases

import (
	"context"
	"errors"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/retention"
	"github.com/gsoultan/panmail/internal/system_settings/entities"
	"github.com/gsoultan/panmail/internal/system_settings/repositories"
)

var defaultRetryPattern = []string{"5m", "15m", "30m", "1h", "3h", "6h", "12h", "24h"}

// ErrNoSettings reports that settings were submitted with nothing in them.
var ErrNoSettings = errors.New("settings are required")

type SettingsUsecase interface {
	GetSettings(ctx context.Context) (*panmailv1.SystemSettings, error)
	UpdateSettings(ctx context.Context, settings *panmailv1.SystemSettings) (*panmailv1.SystemSettings, error)
}

type settingsUsecase struct {
	// Settings live in the database rather than in config.yaml, so a save made
	// on one instance is what every instance enforces. The file version wrote
	// to whichever process served the request and left the others on the old
	// values with nothing to say they had diverged.
	repo repositories.SettingsRepository

	// Notified after a save so a changed retention takes effect now rather
	// than at the next daily pass. Optional: without it the policy still
	// applies, just later.
	retentionWorker RetentionTrigger
}

func NewSettingsUsecase(repo repositories.SettingsRepository, retentionWorker RetentionTrigger) SettingsUsecase {
	return &settingsUsecase{repo: repo, retentionWorker: retentionWorker}
}

func (u *settingsUsecase) GetSettings(ctx context.Context) (*panmailv1.SystemSettings, error) {
	stored, err := u.repo.Get(ctx)
	if err != nil {
		return nil, err
	}

	settings := &panmailv1.SystemSettings{RetryPattern: defaultRetryPattern}
	if stored != nil {
		settings.BaseUrl = stored.BaseURL
		if len(stored.RetryPattern) > 0 {
			settings.RetryPattern = stored.RetryPattern
		}
	}

	// Always the resolved policy, never the raw row. The page has to show what
	// panmail is actually enforcing, and it is also what the form posts back —
	// so a field left untouched must return exactly the value that is already
	// in force.
	applyPolicy(settings, retention.Resolve(stored))
	return settings, nil
}

func (u *settingsUsecase) UpdateSettings(ctx context.Context, s *panmailv1.SystemSettings) (*panmailv1.SystemSettings, error) {
	if s == nil {
		return nil, ErrNoSettings
	}

	// Read first so a field the request does not carry keeps its stored value
	// rather than reverting to the zero value of its column.
	stored, err := u.repo.Get(ctx)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		stored = &entities.Settings{}
	}

	stored.BaseURL = s.BaseUrl
	stored.RetryPattern = s.RetryPattern
	policyOf(s).Apply(stored)

	if err := u.repo.Save(ctx, stored); err != nil {
		return nil, err
	}

	if u.retentionWorker != nil {
		u.retentionWorker.Trigger()
	}

	// Re-read rather than echo the request: what was submitted and what is now
	// in force differ wherever a value was clamped, and an administrator who
	// types 99999 days should see what they actually got.
	return u.GetSettings(ctx)
}

// policyOf reads a retention policy out of a settings message.
func policyOf(s *panmailv1.SystemSettings) retention.Policy {
	return retention.Policy{
		EventDays:      int(s.LogRetentionDays),
		MessageDays:    int(s.MessageRetentionDays),
		OutboxDays:     int(s.OutboxRetentionDays),
		WebhookDays:    int(s.WebhookRetentionDays),
		AppLogDays:     int(s.AppLogRetentionDays),
		InboundDays:    int(s.InboundRetentionDays),
		ArchiveDays:    int(s.ArchiveRetentionDays),
		QuarantineDays: int(s.QuarantineRetentionDays),
	}
}

// applyPolicy writes a resolved policy into a settings message.
func applyPolicy(s *panmailv1.SystemSettings, p retention.Policy) {
	s.LogRetentionDays = int32(p.EventDays)
	s.MessageRetentionDays = int32(p.MessageDays)
	s.OutboxRetentionDays = int32(p.OutboxDays)
	s.WebhookRetentionDays = int32(p.WebhookDays)
	s.AppLogRetentionDays = int32(p.AppLogDays)
	s.InboundRetentionDays = int32(p.InboundDays)
	s.ArchiveRetentionDays = int32(p.ArchiveDays)
	s.QuarantineRetentionDays = int32(p.QuarantineDays)
}
