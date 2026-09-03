package postgres

import (
	"testing"

	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/internal/system_settings/entities"
)

func days(n int) *int { return &n }

func newStore(t *testing.T) *store {
	t.Helper()
	return NewStore(storetest.NewConnection(t)).(*store)
}

func TestGetBeforeAnythingIsStored(t *testing.T) {
	got, err := newStore(t).Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Not an error, and not a zeroed row either: the caller has to be able to
	// tell "nothing configured" from "everything configured to zero", because
	// the second one means keep forever and the first one means use defaults.
	if got != nil {
		t.Fatalf("Get on an empty table = %+v; want nil", got)
	}
}

func TestSaveRoundTripsEveryField(t *testing.T) {
	s := newStore(t)

	want := &entities.Settings{
		BaseURL:                 "https://mail.example.com",
		RetryPattern:            []string{"5m", "1h", "24h"},
		LogRetentionDays:        days(30),
		WebhookRetentionDays:    days(3),
		MessageRetentionDays:    7,
		OutboxRetentionDays:     21,
		AppLogRetentionDays:     5,
		InboundRetentionDays:    90,
		ArchiveRetentionDays:    365,
		QuarantineRetentionDays: 14,
	}
	if err := s.Save(t.Context(), want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get after Save returned nothing")
	}

	if got.BaseURL != want.BaseURL {
		t.Errorf("base url = %q; want %q", got.BaseURL, want.BaseURL)
	}
	if len(got.RetryPattern) != len(want.RetryPattern) {
		t.Fatalf("retry pattern = %v; want %v", got.RetryPattern, want.RetryPattern)
	}
	for i, step := range want.RetryPattern {
		if got.RetryPattern[i] != step {
			t.Errorf("retry pattern[%d] = %q; want %q", i, got.RetryPattern[i], step)
		}
	}

	for _, tc := range []struct {
		name      string
		got, want int
	}{
		{"messages", got.MessageRetentionDays, want.MessageRetentionDays},
		{"outbox", got.OutboxRetentionDays, want.OutboxRetentionDays},
		{"app logs", got.AppLogRetentionDays, want.AppLogRetentionDays},
		{"inbound", got.InboundRetentionDays, want.InboundRetentionDays},
		{"archives", got.ArchiveRetentionDays, want.ArchiveRetentionDays},
		{"quarantine", got.QuarantineRetentionDays, want.QuarantineRetentionDays},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d; want %d", tc.name, tc.got, tc.want)
		}
	}
	if got.LogRetentionDays == nil || *got.LogRetentionDays != 30 {
		t.Errorf("events = %v; want 30", got.LogRetentionDays)
	}
	if got.WebhookRetentionDays == nil || *got.WebhookRetentionDays != 3 {
		t.Errorf("webhooks = %v; want 3", got.WebhookRetentionDays)
	}
}

// The distinction the two nullable columns exist for. Stored as NOT NULL
// DEFAULT 0 these two come back as zero, which resolves to "keep forever" —
// so an administrator who never touched the page would silently stop expiring
// delivery events, and the page would go on showing 14.
func TestUnsetAndZeroAreDifferentValues(t *testing.T) {
	t.Run("never set stays NULL", func(t *testing.T) {
		s := newStore(t)
		if err := s.Save(t.Context(), &entities.Settings{BaseURL: "https://a.example"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := s.Get(t.Context())
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.LogRetentionDays != nil {
			t.Errorf("events = %d; want nil, meaning nobody set one", *got.LogRetentionDays)
		}
		if got.WebhookRetentionDays != nil {
			t.Errorf("webhooks = %d; want nil", *got.WebhookRetentionDays)
		}
	})

	t.Run("a chosen zero survives as zero", func(t *testing.T) {
		s := newStore(t)
		if err := s.Save(t.Context(), &entities.Settings{
			LogRetentionDays:     days(0),
			WebhookRetentionDays: days(0),
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := s.Get(t.Context())
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.LogRetentionDays == nil || *got.LogRetentionDays != 0 {
			t.Errorf("events = %v; want a stored 0", got.LogRetentionDays)
		}
		if got.WebhookRetentionDays == nil || *got.WebhookRetentionDays != 0 {
			t.Errorf("webhooks = %v; want a stored 0", got.WebhookRetentionDays)
		}
	})
}

func TestSaveOverwritesRatherThanInserting(t *testing.T) {
	s := newStore(t)

	if err := s.Save(t.Context(), &entities.Settings{BaseURL: "https://first.example"}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := s.Save(t.Context(), &entities.Settings{BaseURL: "https://second.example"}); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := s.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BaseURL != "https://second.example" {
		t.Errorf("base url = %q; want the second save to win", got.BaseURL)
	}
}

// Every instance runs the seed at startup, so on all but the first the row is
// already there. Overwriting would reset a running deployment's settings to
// whatever is in that instance's config file every time one restarted.
func TestSeedNeverOverwrites(t *testing.T) {
	s := newStore(t)

	seeded, err := s.Seed(t.Context(), &entities.Settings{BaseURL: "https://from-config.example"})
	if err != nil {
		t.Fatalf("first Seed: %v", err)
	}
	if !seeded {
		t.Fatal("the first seed reported it wrote nothing")
	}

	// What an administrator then changes it to.
	if err := s.Save(t.Context(), &entities.Settings{BaseURL: "https://chosen.example"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A second instance starting, or the first one restarting.
	seeded, err = s.Seed(t.Context(), &entities.Settings{BaseURL: "https://from-config.example"})
	if err != nil {
		t.Fatalf("second Seed: %v", err)
	}
	if seeded {
		t.Error("the second seed reported it wrote, which means it overwrote a live setting")
	}

	got, err := s.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BaseURL != "https://chosen.example" {
		t.Errorf("base url = %q; want the administrator's value to survive a restart", got.BaseURL)
	}
}
