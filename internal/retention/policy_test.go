package retention

import (
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/config"
)

func days(n int) *int { return &n }

func TestResolveDefaults(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want Policy
	}{
		{
			name: "no config at all is a first run, not an error",
			cfg:  nil,
			want: Policy{EventDays: DefaultEventDays, WebhookDays: DefaultWebhookDays},
		},
		{
			name: "an empty config keeps everything panmail holds the only copy of",
			cfg:  &config.Config{},
			want: Policy{EventDays: DefaultEventDays, WebhookDays: DefaultWebhookDays},
		},
		{
			// The whole reason those two fields are pointers. Read as a plain
			// int this is indistinguishable from "unset" and silently becomes
			// 14 and 7, so an operator who asked to keep everything forever
			// would keep losing data with the setting showing what they chose.
			name: "an explicit zero means forever, not the default",
			cfg: &config.Config{App: config.AppConfig{
				LogRetentionDays:     days(0),
				WebhookRetentionDays: days(0),
			}},
			want: Policy{},
		},
		{
			name: "configured values win",
			cfg: &config.Config{App: config.AppConfig{
				LogRetentionDays:     days(30),
				WebhookRetentionDays: days(3),
				MessageRetentionDays: 7,
				OutboxRetentionDays:  21,
				AppLogRetentionDays:  5,
				InboundRetentionDays: 90,
				ArchiveRetentionDays: 365,
			}},
			want: Policy{
				EventDays:   30,
				WebhookDays: 3,
				MessageDays: 7,
				OutboxDays:  21,
				AppLogDays:  5,
				InboundDays: 90,
				ArchiveDays: 365,
			},
		},
		{
			name: "out of range folds back to something that deletes no more than asked",
			cfg: &config.Config{App: config.AppConfig{
				LogRetentionDays:     days(MaxDays + 1),
				MessageRetentionDays: -5,
			}},
			want: Policy{EventDays: MaxDays, WebhookDays: DefaultWebhookDays},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.cfg); got != tc.want {
				t.Errorf("Resolve() = %+v; want %+v", got, tc.want)
			}
		})
	}
}

func TestPolicyRoundTripsThroughConfig(t *testing.T) {
	// A deliberate "keep forever" on both pointer fields is the case that has
	// to survive: saving it and reading it back must not hand the defaults
	// back to an administrator who just turned those policies off.
	want := Policy{EventDays: 0, WebhookDays: 0, MessageDays: 30, InboundDays: 90}

	cfg := &config.Config{}
	want.Apply(cfg)

	if got := Resolve(cfg); got != want {
		t.Errorf("Resolve(Apply(%+v)) = %+v; want the same policy back", want, got)
	}
}

func TestCutoff(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		days       int
		wantOK     bool
		wantBefore time.Time
	}{
		{name: "zero keeps forever", days: 0, wantOK: false},
		{name: "negative keeps forever", days: -1, wantOK: false},
		{
			name:       "a positive retention cuts that many days back",
			days:       14,
			wantOK:     true,
			wantBefore: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before, ok := Cutoff(tc.days, now)
			if ok != tc.wantOK {
				t.Fatalf("Cutoff(%d) enabled = %v; want %v", tc.days, ok, tc.wantOK)
			}
			if !ok {
				// The zero time precedes every record, so a caller that
				// ignored ok and used this would delete the store.
				if !before.IsZero() {
					t.Errorf("disabled cutoff = %v; want the zero time", before)
				}
				return
			}
			if !before.Equal(tc.wantBefore) {
				t.Errorf("Cutoff(%d) = %v; want %v", tc.days, before, tc.wantBefore)
			}
		})
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		name string
		days int
		want time.Duration
	}{
		{name: "zero disables pruning", days: 0, want: 0},
		{name: "negative disables pruning", days: -3, want: 0},
		{name: "seven days", days: 7, want: 7 * 24 * time.Hour},
		{name: "clamped at the maximum", days: MaxDays + 100, want: MaxDays * 24 * time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Duration(tc.days); got != tc.want {
				t.Errorf("Duration(%d) = %v; want %v", tc.days, got, tc.want)
			}
		})
	}
}

// The store guards refuse a zero cutoff, so Cutoff must never hand one to a
// caller that ignored its second return. Belt and braces on a delete path.
func TestCutoffNeverReturnsAUsableZeroTime(t *testing.T) {
	for _, days := range []int{0, -1, -3650} {
		before, ok := Cutoff(days, time.Now())
		if ok {
			t.Errorf("Cutoff(%d) enabled pruning", days)
		}
		if !before.IsZero() {
			t.Errorf("Cutoff(%d) = %v; want the zero time the stores refuse", days, before)
		}
	}
}
