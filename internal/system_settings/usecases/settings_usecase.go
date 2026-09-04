package usecases

import (
	"context"
	"errors"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/redact"
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
	// The resolved level, never the raw column, for the same reason retention
	// is resolved: the page has to show what the gateway is enforcing. An unset
	// column shows as PASSWORDS because that is what is actually happening.
	settings.ContentRedaction = redactionToProto(redact.LevelFromString(storedRedaction(stored)))

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

	// UNSPECIFIED from a client means "leave it alone", not "reset to default".
	// Every other field here is full-replace, and this one deliberately is not:
	// a caller updating base_url must not be able to turn redaction off by
	// omission. See the note on partial updates in the retention memory.
	if s.ContentRedaction != panmailv1.ContentRedaction_CONTENT_REDACTION_UNSPECIFIED {
		stored.ContentRedaction = redactionFromProto(s.ContentRedaction).String()
	}

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

// storedRedaction reads the raw column, tolerating a nil row on a first run.
func storedRedaction(s *entities.Settings) string {
	if s == nil {
		return ""
	}
	return s.ContentRedaction
}

// redactionFromProto maps the wire enum onto the engine's level.
func redactionFromProto(v panmailv1.ContentRedaction) redact.Level {
	switch v {
	case panmailv1.ContentRedaction_CONTENT_REDACTION_OFF:
		return redact.Off
	case panmailv1.ContentRedaction_CONTENT_REDACTION_CODES:
		return redact.Codes
	case panmailv1.ContentRedaction_CONTENT_REDACTION_SECRETS:
		return redact.Secrets
	default:
		return redact.Passwords
	}
}

// redactionToProto is the inverse. UNSPECIFIED is never returned: the settings
// page shows what is in force, and "unspecified" is not a behaviour.
func redactionToProto(l redact.Level) panmailv1.ContentRedaction {
	switch l {
	case redact.Off:
		return panmailv1.ContentRedaction_CONTENT_REDACTION_OFF
	case redact.Codes:
		return panmailv1.ContentRedaction_CONTENT_REDACTION_CODES
	case redact.Secrets:
		return panmailv1.ContentRedaction_CONTENT_REDACTION_SECRETS
	default:
		return panmailv1.ContentRedaction_CONTENT_REDACTION_PASSWORDS
	}
}
