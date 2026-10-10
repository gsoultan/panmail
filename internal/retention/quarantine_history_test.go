package retention

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/system_settings/entities"
)

type fakeQuarantineHistory struct{ last call }

func (f *fakeQuarantineHistory) PruneBefore(_ context.Context, before time.Time) (int64, error) {
	f.last = call{before: before, ran: true}
	return 1, nil
}

// The review history and the held payloads nothing discarded are pruned on the
// quarantine policy. Before this nothing deleted either, whatever the policy.
func TestQuarantineHistoryIsPrunedOnTheQuarantinePolicy(t *testing.T) {
	history := &fakeQuarantineHistory{}
	w, now := workerFor(&entities.Settings{QuarantineRetentionDays: 30}, Deps{QuarantineHistory: history})

	w.RunOnce(t.Context())

	if !history.last.ran {
		t.Fatal("the quarantine history was not pruned")
	}
	if want := now.AddDate(0, 0, -30); !history.last.before.Equal(want) {
		t.Errorf("cutoff = %v, want %v", history.last.before, want)
	}
}

// Zero is forever here as everywhere.
func TestAQuarantinePolicyOfZeroKeepsTheHistory(t *testing.T) {
	history := &fakeQuarantineHistory{}
	w, _ := workerFor(&entities.Settings{QuarantineRetentionDays: 0}, Deps{QuarantineHistory: history})

	w.RunOnce(t.Context())

	if history.last.ran {
		t.Error("a quarantine policy of zero pruned the history")
	}
}
