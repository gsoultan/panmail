package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	systemsettings "github.com/gsoultan/panmail/internal/system_settings"
	"google.golang.org/protobuf/proto"
)

// The instance that served a save has to apply it at once. The provider only
// re-read on its own one-minute timer, so for up to a minute after the page
// said "saved", this instance kept putting the old base URL into every
// tracking and unsubscribe link it sent.
func TestASaveIsInForceOnThisInstanceImmediately(t *testing.T) {
	repo := &memoryRepo{}
	provider := systemsettings.NewProvider(repo, time.Hour)
	if err := provider.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	u := NewSettingsUsecase(repo, nil, provider)

	if _, err := u.UpdateSettings(t.Context(), &panmailv1.SystemSettings{BaseUrl: proto.String("https://mail.example.com/")}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := provider.BaseURL(); got != "https://mail.example.com" {
		t.Errorf("BaseURL() = %q right after the save, want the saved value", got)
	}
}

type failingRefresher struct{ calls int }

func (f *failingRefresher) Refresh(context.Context) error {
	f.calls++
	return errors.New("database is down")
}

// The row is saved by then; failing the request would report a saved change
// as not saved.
func TestARefreshFailureDoesNotFailTheSave(t *testing.T) {
	repo := &memoryRepo{}
	refresher := &failingRefresher{}
	u := NewSettingsUsecase(repo, nil, refresher)

	if _, err := u.UpdateSettings(t.Context(), &panmailv1.SystemSettings{BaseUrl: proto.String("https://a.example")}); err != nil {
		t.Fatalf("update failed because the refresh did: %v", err)
	}
	if refresher.calls != 1 {
		t.Errorf("Refresh called %d times, want 1", refresher.calls)
	}
}

// updated_at was read back with the row and written back unchanged, so it
// recorded the first save forever.
func TestEverySaveAdvancesUpdatedAt(t *testing.T) {
	repo := &memoryRepo{}
	clock := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	u := NewSettingsUsecase(repo, nil, nil)
	u.(*settingsUsecase).now = func() time.Time { return clock }

	if _, err := u.UpdateSettings(t.Context(), &panmailv1.SystemSettings{BaseUrl: proto.String("https://a.example")}); err != nil {
		t.Fatal(err)
	}
	if !repo.row.UpdatedAt.Equal(clock) {
		t.Fatalf("updated_at = %v after the first save, want %v", repo.row.UpdatedAt, clock)
	}

	clock = clock.Add(time.Hour)
	if _, err := u.UpdateSettings(t.Context(), &panmailv1.SystemSettings{BaseUrl: proto.String("https://b.example")}); err != nil {
		t.Fatal(err)
	}
	if !repo.row.UpdatedAt.Equal(clock) {
		t.Errorf("updated_at = %v after the second save, want %v", repo.row.UpdatedAt, clock)
	}
}
