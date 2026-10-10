package retention

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/system_settings/entities"
)

type fakeTelemetryEvents struct {
	fakeEvents
	minutes   call
	resources call
}

func (f *fakeTelemetryEvents) PruneMinuteSeriesBefore(_ context.Context, before time.Time) (int64, error) {
	f.minutes = call{before: before, ran: true}
	return 1, nil
}

func (f *fakeTelemetryEvents) PruneResourceHistoryBefore(_ context.Context, before time.Time) (int64, error) {
	f.resources = call{before: before, ran: true}
	return 1, nil
}

// Minute counters and resource samples are written for the life of the
// process and were never pruned by anything. They have fixed horizons rather
// than a policy, so they are pruned on every pass whatever the policy says —
// including a deployment that keeps every class forever, which is exactly the
// one whose disk this would otherwise fill.
func TestTelemetryIsPrunedOnItsOwnHorizon(t *testing.T) {
	zero := 0
	tests := []struct {
		name string
		cfg  *entities.Settings
	}{
		{"defaults", nil},
		{"everything kept forever", &entities.Settings{LogRetentionDays: &zero, WebhookRetentionDays: &zero}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := &fakeTelemetryEvents{}
			w, now := workerFor(tt.cfg, Deps{Events: events})
			w.RunOnce(t.Context())

			if want := now.AddDate(0, 0, -MinuteSeriesDays); !events.minutes.ran || !events.minutes.before.Equal(want) {
				t.Errorf("minute series pruned=%v before %v; want before %v",
					events.minutes.ran, events.minutes.before, want)
			}
			if want := now.AddDate(0, 0, -ResourceHistoryDays); !events.resources.ran || !events.resources.before.Equal(want) {
				t.Errorf("resource history pruned=%v before %v; want before %v",
					events.resources.ran, events.resources.before, want)
			}
		})
	}
}

// A store without telemetry is not asked for it.
func TestAStoreWithoutTelemetryIsLeftAlone(t *testing.T) {
	events := &fakeEvents{}
	w, _ := workerFor(nil, Deps{Events: events})
	w.RunOnce(t.Context())
	if !events.events.ran {
		t.Error("the event policy did not run")
	}
}

type fakeReceipts struct{ call }

func (f *fakeReceipts) PruneBefore(_ context.Context, before time.Time) (int64, error) {
	f.call = call{before: before, ran: true}
	return 1, nil
}

// The inbound endpoint's receipts are written on every post and were the one
// table it added with no bound. Like telemetry they have a fixed horizon, so
// they are pruned even when received mail itself is kept forever.
func TestInboundReceiptsArePrunedOnTheirOwnHorizon(t *testing.T) {
	receipts := &fakeReceipts{}
	w, now := workerFor(&entities.Settings{InboundRetentionDays: 0}, Deps{InboundReceipts: receipts})
	w.RunOnce(t.Context())

	if want := now.AddDate(0, 0, -InboundReceiptDays); !receipts.ran || !receipts.before.Equal(want) {
		t.Errorf("receipts pruned=%v before %v; want before %v", receipts.ran, receipts.before, want)
	}
}
