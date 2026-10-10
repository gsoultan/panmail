package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/storetest"
)

// backfillingLocal is a local store that records provider backfills.
type backfillingLocal struct {
	localStub
	backfilled []string
}

func (b *backfillingLocal) BackfillMessageProvider(_ context.Context, _, messageID, _ string) error {
	b.backfilled = append(b.backfilled, messageID)
	return nil
}

// The provider is set with one UPDATE of one column, only where none is
// recorded yet, and mirrored to the local store like the body it belongs to.
func TestBackfillMessageProviderSetsAMissingProviderOnly(t *testing.T) {
	conn := storetest.NewConnection(t)
	local := &backfillingLocal{}
	s := NewStore(conn, local, NewWriter(conn))

	bare := &entities.EmailMessage{ID: storetest.ID("bare"), TenantID: storetest.TenantA,
		From: "a@example.com", CreatedAt: time.Now().UTC()}
	named := &entities.EmailMessage{ID: storetest.ID("named"), TenantID: storetest.TenantA,
		ProviderID: storetest.ID("first"), From: "a@example.com", CreatedAt: time.Now().UTC()}
	for _, m := range []*entities.EmailMessage{bare, named} {
		if err := s.WriteMessage(t.Context(), m); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
	}

	for _, id := range []string{bare.ID, named.ID} {
		if err := s.BackfillMessageProvider(t.Context(), storetest.TenantA, id, storetest.ID("later")); err != nil {
			t.Fatalf("backfill %s: %v", id, err)
		}
	}

	for _, tt := range []struct {
		id, want string
	}{
		{bare.ID, storetest.ID("later")},
		{named.ID, storetest.ID("first")},
	} {
		got, err := s.GetMessage(t.Context(), storetest.TenantA, tt.id)
		if err != nil || got == nil {
			t.Fatalf("GetMessage: %v", err)
		}
		if got.ProviderID != tt.want {
			t.Errorf("provider = %q; want %q", got.ProviderID, tt.want)
		}
	}
	if len(local.backfilled) != 2 {
		t.Errorf("mirrored %d backfills to the local store; want 2", len(local.backfilled))
	}
}
