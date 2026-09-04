package usecases

import (
	"context"
	"errors"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/redact"
	"github.com/gsoultan/panmail/internal/retention"
	"github.com/gsoultan/panmail/internal/system_settings/entities"
	"github.com/gsoultan/panmail/internal/system_settings/repositories"
	"google.golang.org/protobuf/proto"
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

	// A read always sets every field. Presence means "the caller mentioned it"
	// on the way in; on the way out the page needs the whole picture, so nothing
	// is left absent here.
	settings := &panmailv1.SystemSettings{RetryPattern: defaultRetryPattern}
	settings.BaseUrl = proto.String("")
	if stored != nil {
		settings.BaseUrl = proto.String(stored.BaseURL)
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

	// Apply only what the request carries. A field the caller did not mention
	// keeps the value it had.
	//
	// This is the whole point of the presence tracking on these fields. When
	// they were plain scalars, an update of base_url also wrote zero to all
	// seven retention policies — and zero means keep forever, so retention
	// stopped with no error and no log line. "Keep forever" and "I did not
	// mention it" are opposite instructions and a bare int32 renders them
	// identically.
	if s.BaseUrl != nil {
		stored.BaseURL = s.GetBaseUrl()
	}
	// Repeated fields have no presence, so empty means "leave it alone". Losing
	// the ability to clear it costs nothing: an empty stored pattern reads back
	// as the built-in default, so the default is expressible by sending it.
	if len(s.RetryPattern) > 0 {
		stored.RetryPattern = s.RetryPattern
	}

	applyPresentRetention(s, stored)

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

// applyPresentRetention writes only the retention fields the request carries.
//
// It starts from what is stored, so an omitted field survives, and clamps
// through retention.Policy so a value out of range folds the same way it always
// did. Every field is listed: the version of Policy.Apply that forgot
// QuarantineDays shipped a setting that silently never saved, and the lesson
// was that a mapping like this is only as complete as its longest list.
func applyPresentRetention(s *panmailv1.SystemSettings, stored *entities.Settings) {
	p := retention.Resolve(stored)

	for _, f := range []struct {
		value *int32
		field *int
	}{
		{s.LogRetentionDays, &p.EventDays},
		{s.MessageRetentionDays, &p.MessageDays},
		{s.OutboxRetentionDays, &p.OutboxDays},
		{s.WebhookRetentionDays, &p.WebhookDays},
		{s.AppLogRetentionDays, &p.AppLogDays},
		{s.InboundRetentionDays, &p.InboundDays},
		{s.ArchiveRetentionDays, &p.ArchiveDays},
		{s.QuarantineRetentionDays, &p.QuarantineDays},
	} {
		if f.value != nil {
			*f.field = int(*f.value)
		}
	}

	p.Apply(stored)
}

// applyPolicy writes a resolved policy into a settings message. Every field is
// set, because a read reports what is in force rather than what was asked for.
func applyPolicy(s *panmailv1.SystemSettings, p retention.Policy) {
	s.LogRetentionDays = proto.Int32(int32(p.EventDays))
	s.MessageRetentionDays = proto.Int32(int32(p.MessageDays))
	s.OutboxRetentionDays = proto.Int32(int32(p.OutboxDays))
	s.WebhookRetentionDays = proto.Int32(int32(p.WebhookDays))
	s.AppLogRetentionDays = proto.Int32(int32(p.AppLogDays))
	s.InboundRetentionDays = proto.Int32(int32(p.InboundDays))
	s.ArchiveRetentionDays = proto.Int32(int32(p.ArchiveDays))
	s.QuarantineRetentionDays = proto.Int32(int32(p.QuarantineDays))
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
