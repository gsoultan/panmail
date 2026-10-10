package retention

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/system_settings/entities"
)

type fakeAnalyticsEvents struct {
	fakeTelemetryEvents
	hourDay call
}

func (f *fakeAnalyticsEvents) PruneHourDaySeriesBefore(_ context.Context, before time.Time) (int64, error) {
	f.hourDay = call{before: before, ran: true}
	return 1, nil
}

// Hour and day counters are what a long chart is drawn from and are kept
// forever unless an administrator sets a horizon. Forever is the default, so
// an upgrade prunes nothing; a horizon prunes them, and pulls the minute
// counters in with it when it is the shorter of the two.
func TestAnalyticsCountersFollowTheirRetention(t *testing.T) {
	tests := []struct {
		name        string
		days        int
		wantHourDay int // 0: not pruned
		wantMinutes int
	}{
		{"forever by default", 0, 0, MinuteSeriesDays},
		{"a year", 365, 365, MinuteSeriesDays},
		{"shorter than the minute horizon", 7, 7, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := &fakeAnalyticsEvents{}
			w, now := workerFor(&entities.Settings{AnalyticsRetentionDays: tt.days}, Deps{Events: events})
			w.RunOnce(t.Context())

			if tt.wantHourDay == 0 {
				if events.hourDay.ran {
					t.Errorf("hour and day counters pruned before %v with no retention set", events.hourDay.before)
				}
			} else if want := now.AddDate(0, 0, -tt.wantHourDay); !events.hourDay.ran || !events.hourDay.before.Equal(want) {
				t.Errorf("hour and day counters pruned=%v before %v; want before %v",
					events.hourDay.ran, events.hourDay.before, want)
			}
			if want := now.AddDate(0, 0, -tt.wantMinutes); !events.minutes.ran || !events.minutes.before.Equal(want) {
				t.Errorf("minute series pruned=%v before %v; want before %v",
					events.minutes.ran, events.minutes.before, want)
			}
		})
	}
}
