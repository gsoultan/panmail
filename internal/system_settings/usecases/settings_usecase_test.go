package usecases

import (
	"path/filepath"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/internal/retention"
)

// inTempConfig points config at a file this test owns, so saving settings does
// not write to the developer's own ~/.panmail.
func inTempConfig(t *testing.T) {
	t.Helper()

	config.SetConfigPath(filepath.Join(t.TempDir(), "db_config.yaml"))
	t.Cleanup(func() { config.SetConfigPath("") })
}

type spyTrigger struct{ calls int }

func (s *spyTrigger) Trigger() { s.calls++ }

func TestGetSettingsOnAFirstRun(t *testing.T) {
	inTempConfig(t)

	got, err := NewSettingsUsecase(nil).GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}

	// Before setup has written anything, the page has to show what the
	// gateway is actually enforcing rather than a row of zeroes that would
	// read as "nothing is ever deleted".
	if got.LogRetentionDays != retention.DefaultEventDays {
		t.Errorf("events = %d; want %d", got.LogRetentionDays, retention.DefaultEventDays)
	}
	if got.WebhookRetentionDays != retention.DefaultWebhookDays {
		t.Errorf("webhooks = %d; want %d", got.WebhookRetentionDays, retention.DefaultWebhookDays)
	}
	if got.MessageRetentionDays != 0 {
		t.Errorf("message content = %d; want 0, which is keep forever", got.MessageRetentionDays)
	}
	if len(got.RetryPattern) == 0 {
		t.Error("retry pattern came back empty")
	}
}

func TestUpdateSettingsRoundTrip(t *testing.T) {
	inTempConfig(t)

	trigger := &spyTrigger{}
	usecase := NewSettingsUsecase(trigger)

	saved, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		BaseUrl:              "https://mail.example.com",
		RetryPattern:         []string{"5m", "1h"},
		LogRetentionDays:     30,
		MessageRetentionDays: 7,
		OutboxRetentionDays:  21,
		WebhookRetentionDays: 3,
		AppLogRetentionDays:  5,
		InboundRetentionDays: 90,
		ArchiveRetentionDays: 365,
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	// A saved retention that only applies at the next daily pass reads, from
	// the settings page, as a setting that did nothing.
	if trigger.calls != 1 {
		t.Errorf("retention triggered %d times; want 1", trigger.calls)
	}

	reloaded, err := usecase.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}

	for _, tc := range []struct {
		name        string
		saved, read int32
		want        int32
	}{
		{"events", saved.LogRetentionDays, reloaded.LogRetentionDays, 30},
		{"messages", saved.MessageRetentionDays, reloaded.MessageRetentionDays, 7},
		{"outbox", saved.OutboxRetentionDays, reloaded.OutboxRetentionDays, 21},
		{"webhooks", saved.WebhookRetentionDays, reloaded.WebhookRetentionDays, 3},
		{"app logs", saved.AppLogRetentionDays, reloaded.AppLogRetentionDays, 5},
		{"inbound", saved.InboundRetentionDays, reloaded.InboundRetentionDays, 90},
		{"archives", saved.ArchiveRetentionDays, reloaded.ArchiveRetentionDays, 365},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.saved != tc.want {
				t.Errorf("returned %d; want %d", tc.saved, tc.want)
			}
			if tc.read != tc.want {
				t.Errorf("read back %d; want %d", tc.read, tc.want)
			}
		})
	}

	if reloaded.BaseUrl != "https://mail.example.com" {
		t.Errorf("base url = %q; want it preserved", reloaded.BaseUrl)
	}
}

func TestUpdateSettingsKeepsADeliberateForever(t *testing.T) {
	inTempConfig(t)

	usecase := NewSettingsUsecase(nil)
	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		LogRetentionDays:     0,
		WebhookRetentionDays: 0,
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	got, err := usecase.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}

	// The trap the pointer fields in AppConfig exist to avoid. Stored as plain
	// ints these two come back as 14 and 7 — an administrator who chose to
	// keep everything would go on losing data, with the page showing the
	// choice they made.
	if got.LogRetentionDays != 0 {
		t.Errorf("events = %d; want the configured 0 to survive", got.LogRetentionDays)
	}
	if got.WebhookRetentionDays != 0 {
		t.Errorf("webhooks = %d; want the configured 0 to survive", got.WebhookRetentionDays)
	}
}

func TestUpdateSettingsClampsAndReportsWhatWasStored(t *testing.T) {
	inTempConfig(t)

	got, err := NewSettingsUsecase(nil).UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		MessageRetentionDays: retention.MaxDays + 500,
		InboundRetentionDays: -30,
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	// Echoing the request back would tell an administrator they got what they
	// typed. They did not.
	if got.MessageRetentionDays != retention.MaxDays {
		t.Errorf("messages = %d; want it clamped to %d", got.MessageRetentionDays, retention.MaxDays)
	}
	// Negative is nonsense rather than an instruction to delete everything.
	if got.InboundRetentionDays != 0 {
		t.Errorf("inbound = %d; want a negative retention to fold to keep-forever", got.InboundRetentionDays)
	}
}

func TestUpdateSettingsRejectsNothing(t *testing.T) {
	inTempConfig(t)

	if _, err := NewSettingsUsecase(nil).UpdateSettings(t.Context(), nil); err == nil {
		t.Error("accepted a request with no settings in it")
	}
}
