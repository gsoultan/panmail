package usecases

import (
	"context"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/retention"
	"github.com/gsoultan/panmail/internal/system_settings/entities"
	"google.golang.org/protobuf/proto"
)

// memoryRepo is the settings row, in memory. It stores a copy rather than the
// caller's pointer, so a test cannot pass by mutating the value it handed in.
type memoryRepo struct {
	row *entities.Settings
}

func (r *memoryRepo) Get(context.Context) (*entities.Settings, error) {
	if r.row == nil {
		return nil, nil
	}
	clone := *r.row
	return &clone, nil
}

func (r *memoryRepo) Save(_ context.Context, s *entities.Settings) error {
	clone := *s
	r.row = &clone
	return nil
}

func (r *memoryRepo) Seed(_ context.Context, s *entities.Settings) (bool, error) {
	if r.row != nil {
		return false, nil
	}
	clone := *s
	r.row = &clone
	return true, nil
}

// newUsecase builds the usecase over an empty in-memory row, which is what a
// deployment that has never opened the settings page has.
func newUsecase(trigger RetentionTrigger) (SettingsUsecase, *memoryRepo) {
	repo := &memoryRepo{}
	return NewSettingsUsecase(repo, trigger), repo
}

type spyTrigger struct{ calls int }

func (s *spyTrigger) Trigger() { s.calls++ }

func TestGetSettingsOnAFirstRun(t *testing.T) {
	usecase, _ := newUsecase(nil)

	got, err := usecase.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}

	// Before setup has written anything, the page has to show what the
	// gateway is actually enforcing rather than a row of zeroes that would
	// read as "nothing is ever deleted".
	if got.GetLogRetentionDays() != retention.DefaultEventDays {
		t.Errorf("events = %d; want %d", got.GetLogRetentionDays(), retention.DefaultEventDays)
	}
	if got.GetWebhookRetentionDays() != retention.DefaultWebhookDays {
		t.Errorf("webhooks = %d; want %d", got.GetWebhookRetentionDays(), retention.DefaultWebhookDays)
	}
	if got.GetMessageRetentionDays() != 0 {
		t.Errorf("message content = %d; want 0, which is keep forever", got.GetMessageRetentionDays())
	}
	if len(got.RetryPattern) == 0 {
		t.Error("retry pattern came back empty")
	}
}

func TestUpdateSettingsRoundTrip(t *testing.T) {
	trigger := &spyTrigger{}
	usecase, _ := newUsecase(trigger)

	saved, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		BaseUrl:              proto.String("https://mail.example.com"),
		RetryPattern:         []string{"5m", "1h"},
		LogRetentionDays:     proto.Int32(30),
		MessageRetentionDays: proto.Int32(7),
		OutboxRetentionDays:  proto.Int32(21),
		WebhookRetentionDays: proto.Int32(3),
		AppLogRetentionDays:  proto.Int32(5),
		InboundRetentionDays: proto.Int32(90),
		ArchiveRetentionDays: proto.Int32(365),
		// Included deliberately. Left out of this list, the settings page could
		// go on dropping it silently — which is exactly what happened while
		// Policy.Apply forgot the field and every fixture here left it at zero.
		QuarantineRetentionDays: proto.Int32(14),
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
		{"events", saved.GetLogRetentionDays(), reloaded.GetLogRetentionDays(), 30},
		{"messages", saved.GetMessageRetentionDays(), reloaded.GetMessageRetentionDays(), 7},
		{"outbox", saved.GetOutboxRetentionDays(), reloaded.GetOutboxRetentionDays(), 21},
		{"webhooks", saved.GetWebhookRetentionDays(), reloaded.GetWebhookRetentionDays(), 3},
		{"app logs", saved.GetAppLogRetentionDays(), reloaded.GetAppLogRetentionDays(), 5},
		{"inbound", saved.GetInboundRetentionDays(), reloaded.GetInboundRetentionDays(), 90},
		{"archives", saved.GetArchiveRetentionDays(), reloaded.GetArchiveRetentionDays(), 365},
		{"quarantine", saved.GetQuarantineRetentionDays(), reloaded.GetQuarantineRetentionDays(), 14},
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

	if reloaded.GetBaseUrl() != "https://mail.example.com" {
		t.Errorf("base url = %q; want it preserved", reloaded.GetBaseUrl())
	}
}

func TestUpdateSettingsKeepsADeliberateForever(t *testing.T) {
	usecase, _ := newUsecase(nil)
	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		LogRetentionDays:     proto.Int32(0),
		WebhookRetentionDays: proto.Int32(0),
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
	if got.GetLogRetentionDays() != 0 {
		t.Errorf("events = %d; want the configured 0 to survive", got.GetLogRetentionDays())
	}
	if got.GetWebhookRetentionDays() != 0 {
		t.Errorf("webhooks = %d; want the configured 0 to survive", got.GetWebhookRetentionDays())
	}
}

func TestUpdateSettingsClampsAndReportsWhatWasStored(t *testing.T) {
	usecase, _ := newUsecase(nil)

	got, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		MessageRetentionDays: proto.Int32(retention.MaxDays + 500),
		InboundRetentionDays: proto.Int32(-30),
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	// Echoing the request back would tell an administrator they got what they
	// typed. They did not.
	if got.GetMessageRetentionDays() != retention.MaxDays {
		t.Errorf("messages = %d; want it clamped to %d", got.GetMessageRetentionDays(), retention.MaxDays)
	}
	// Negative is nonsense rather than an instruction to delete everything.
	if got.GetInboundRetentionDays() != 0 {
		t.Errorf("inbound = %d; want a negative retention to fold to keep-forever", got.GetInboundRetentionDays())
	}
}

func TestUpdateSettingsRejectsNothing(t *testing.T) {
	usecase, _ := newUsecase(nil)

	if _, err := usecase.UpdateSettings(t.Context(), nil); err == nil {
		t.Error("accepted a request with no settings in it")
	}
}

// The bug this presence tracking exists for.
//
// Before it, every scalar was full-replace: a caller changing base_url wrote
// zero to all seven retention policies, and zero means keep forever — so
// retention stopped with no error, no log line, and nothing to notice until a
// disk filled. Found by doing exactly this during a soak and watching 1.3 GB
// accumulate unpruned.
func TestAPartialUpdateDoesNotWipeRetention(t *testing.T) {
	usecase, _ := newUsecase(nil)

	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		LogRetentionDays:        proto.Int32(30),
		MessageRetentionDays:    proto.Int32(7),
		OutboxRetentionDays:     proto.Int32(21),
		WebhookRetentionDays:    proto.Int32(3),
		AppLogRetentionDays:     proto.Int32(5),
		InboundRetentionDays:    proto.Int32(90),
		ArchiveRetentionDays:    proto.Int32(365),
		QuarantineRetentionDays: proto.Int32(14),
	}); err != nil {
		t.Fatalf("configuring retention: %v", err)
	}

	// A later caller changing one unrelated field, exactly as a script would.
	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		BaseUrl: proto.String("https://mail.example.com"),
	}); err != nil {
		t.Fatalf("partial update: %v", err)
	}

	got, err := usecase.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}

	for _, tc := range []struct {
		name string
		got  int32
		want int32
	}{
		{"events", got.GetLogRetentionDays(), 30},
		{"messages", got.GetMessageRetentionDays(), 7},
		{"outbox", got.GetOutboxRetentionDays(), 21},
		{"webhooks", got.GetWebhookRetentionDays(), 3},
		{"app logs", got.GetAppLogRetentionDays(), 5},
		{"inbound", got.GetInboundRetentionDays(), 90},
		{"archives", got.GetArchiveRetentionDays(), 365},
		{"quarantine", got.GetQuarantineRetentionDays(), 14},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d after an update that never mentioned it; want %d",
				tc.name, tc.got, tc.want)
		}
	}
	if got.GetBaseUrl() != "https://mail.example.com" {
		t.Errorf("base url = %q; the update should still have applied", got.GetBaseUrl())
	}
}

// Presence is what separates the two. An explicit zero still means forever,
// which is the whole reason a plain int32 could not express this.
func TestAnExplicitZeroStillMeansForever(t *testing.T) {
	usecase, _ := newUsecase(nil)

	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		LogRetentionDays: proto.Int32(30),
	}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		LogRetentionDays: proto.Int32(0),
	}); err != nil {
		t.Fatalf("second update: %v", err)
	}

	got, err := usecase.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.GetLogRetentionDays() != 0 {
		t.Errorf("events = %d; a deliberate 0 must survive as keep-forever",
			got.GetLogRetentionDays())
	}
}

// An empty repeated field has no presence, so it means "leave it alone".
// Nothing is lost: an empty stored pattern reads back as the built-in default.
func TestAnOmittedRetryPatternIsLeftAlone(t *testing.T) {
	usecase, _ := newUsecase(nil)

	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		RetryPattern: []string{"1m", "2m"},
	}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		BaseUrl: proto.String("https://mail.example.com"),
	}); err != nil {
		t.Fatalf("partial update: %v", err)
	}

	got, err := usecase.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if len(got.RetryPattern) != 2 || got.RetryPattern[0] != "1m" {
		t.Errorf("retry pattern = %v; want the configured one to survive", got.RetryPattern)
	}
}
