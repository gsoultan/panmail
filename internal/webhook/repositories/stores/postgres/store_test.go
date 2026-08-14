package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores"
	"github.com/gsoultan/panmail/pkg/secrets"
)

var fixedTime = time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)

func newRepo(t *testing.T) stores.WebhookRepository {
	t.Helper()
	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyring, err := secrets.NewKeyring(key)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return NewStore(storetest.NewConnection(t), keyring)
}

func webhook(id, tenantID, name string, events []int32, active bool) *entities.Webhook {
	// The fixture is named for readability; the column is a UUID on PostgreSQL
	// and merely a VARCHAR on SQLite. Mapping here keeps call sites saying
	// "k1" while the database gets something it will accept — the difference
	// that let these fixtures pass for as long as only SQLite was run.
	id = storetest.ID(id)

	return &entities.Webhook{
		ID:        id,
		TenantID:  tenantID,
		Name:      name,
		URL:       "https://hooks.example.com/" + id,
		Events:    events,
		Active:    active,
		CreatedAt: fixedTime,
		UpdatedAt: fixedTime,
	}
}

// Events are a []int32 stored as JSON, which is the one field in this entity
// that cannot round trip by accident.
func TestCreateAndGetRoundTripsTheEventList(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	want := webhook("w1", storetest.TenantA, "Deliveries", []int32{1, 2, 5}, true)
	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByID(ctx, storetest.TenantA, storetest.ID("w1"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected the webhook back")
	}
	if got.Name != want.Name || got.URL != want.URL || got.Active != want.Active {
		t.Errorf("round trip lost data: %+v", got)
	}
	if len(got.Events) != len(want.Events) {
		t.Fatalf("event list lost entries: got %v, want %v", got.Events, want.Events)
	}
	for i := range want.Events {
		if got.Events[i] != want.Events[i] {
			t.Errorf("event %d changed: got %d, want %d", i, got.Events[i], want.Events[i])
		}
	}
}

func TestEmptyEventListRoundTrips(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, webhook("w1", storetest.TenantA, "None", []int32{}, true)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByID(ctx, storetest.TenantA, storetest.ID("w1"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected the webhook back")
	}
	if len(got.Events) != 0 {
		t.Errorf("expected no events, got %v", got.Events)
	}
}

// A webhook receives message content, so delivering one tenant's events to
// another tenant's endpoint would be a data leak to a third party.
func TestWebhooksAreScopedToTheirTenant(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, webhook("mine", storetest.TenantA, "Mine", []int32{1}, true)); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := repo.Create(ctx, webhook("theirs", storetest.TenantB, "Theirs", []int32{1}, true)); err != nil {
		t.Fatalf("create B: %v", err)
	}

	t.Run("GetByID", func(t *testing.T) {
		got, err := repo.GetByID(ctx, storetest.TenantA, storetest.ID("theirs"))
		if err == nil && got != nil {
			t.Errorf("tenant A read tenant B's webhook: %+v", got)
		}
	})

	t.Run("List", func(t *testing.T) {
		got, _, err := repo.List(ctx, storetest.TenantA, 50, "")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, w := range got {
			if w.TenantID != storetest.TenantA {
				t.Errorf("list leaked a webhook from %s", w.TenantID)
			}
		}
	})

	// This is the method the delivery path actually calls, so a missing tenant
	// filter here posts one tenant's events to another tenant's endpoint.
	t.Run("ListActiveByEvent", func(t *testing.T) {
		got, err := repo.ListActiveByEvent(ctx, storetest.TenantA, 1)
		if err != nil {
			t.Fatalf("list active: %v", err)
		}
		for _, w := range got {
			if w.TenantID != storetest.TenantA {
				t.Errorf("event dispatch would reach %s's endpoint %s", w.TenantID, w.URL)
			}
		}
		if len(got) != 1 {
			t.Errorf("expected 1 active webhook for tenant A, got %d", len(got))
		}
	})

	t.Run("Delete", func(t *testing.T) {
		_ = repo.Delete(ctx, storetest.TenantA, storetest.ID("theirs"))
		victim, err := repo.GetByID(ctx, storetest.TenantB, storetest.ID("theirs"))
		if err != nil {
			t.Fatalf("get B: %v", err)
		}
		if victim == nil {
			t.Error("tenant A deleted tenant B's webhook")
		}
	})
}

func TestListActiveByEventFiltersOnBothActiveAndEvent(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, webhook("on", storetest.TenantA, "On", []int32{7}, true)); err != nil {
		t.Fatalf("create on: %v", err)
	}
	if err := repo.Create(ctx, webhook("off", storetest.TenantA, "Off", []int32{7}, false)); err != nil {
		t.Fatalf("create off: %v", err)
	}
	if err := repo.Create(ctx, webhook("other", storetest.TenantA, "Other", []int32{9}, true)); err != nil {
		t.Fatalf("create other: %v", err)
	}

	got, err := repo.ListActiveByEvent(ctx, storetest.TenantA, 7)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(got) != 1 || got[0].ID != storetest.ID("on") {
		ids := make([]string, len(got))
		for i, w := range got {
			ids[i] = w.ID
		}
		t.Errorf("expected only the active webhook subscribed to event 7, got %v", ids)
	}
}

// Subscribing to event 1 must not match event 11 or 21. A JSON array stored as
// text invites a LIKE '%1%' style filter, which would.
func TestEventMatchingIsNotSubstringMatching(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, webhook("w11", storetest.TenantA, "Eleven", []int32{11}, true)); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.ListActiveByEvent(ctx, storetest.TenantA, 1)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("event 1 matched a webhook subscribed only to event 11: %+v", got)
	}
}

func TestUpdateChangesTheStoredRow(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, webhook("w1", storetest.TenantA, "Before", []int32{1}, true)); err != nil {
		t.Fatalf("create: %v", err)
	}

	updated := webhook("w1", storetest.TenantA, "After", []int32{2, 3}, false)
	if err := repo.Update(ctx, updated); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.GetByID(ctx, storetest.TenantA, storetest.ID("w1"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "After" || got.Active {
		t.Errorf("update did not persist: %+v", got)
	}
	if len(got.Events) != 2 {
		t.Errorf("event list did not update: %v", got.Events)
	}
}

// A rotation that misses a store only shows up after the old key is dropped:
// those rows can no longer be decrypted, and whatever depends on them stops
// working with no obvious cause. Webhook signing secrets were that gap.
func TestRotateSecretsMovesSigningSecretsOntoTheNewKey(t *testing.T) {
	conn := storetest.NewConnection(t)
	ctx := context.Background()

	oldKey, newKey := mustGenerateKey(t), mustGenerateKey(t)

	before := NewStore(conn, mustKeyring(t, oldKey))
	if err := before.Create(ctx, &entities.Webhook{
		ID: storetest.ID("wh-1"), TenantID: storetest.TenantA, Name: "probe",
		URL: "https://example.com/hook", Events: []int32{1}, Active: true,
		Secret: "the-signing-secret",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	after := NewStore(conn, mustKeyring(t, newKey, oldKey))
	rotated, err := after.(interface {
		RotateSecrets(context.Context) (int, error)
	}).RotateSecrets(ctx)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated != 1 {
		t.Errorf("rotated %d webhooks, want 1", rotated)
	}

	// The point of the rotation: the old key is now genuinely unnecessary.
	final := NewStore(conn, mustKeyring(t, newKey))
	w, err := final.GetByID(ctx, storetest.TenantA, storetest.ID("wh-1"))
	if err != nil {
		t.Fatalf("read after rotation without the old key: %v", err)
	}
	if w.Secret != "the-signing-secret" {
		t.Errorf("secret = %q, want it preserved", w.Secret)
	}
}

func TestRotatingWebhookSecretsIsIdempotent(t *testing.T) {
	conn := storetest.NewConnection(t)
	ctx := context.Background()
	oldKey, newKey := mustGenerateKey(t), mustGenerateKey(t)

	before := NewStore(conn, mustKeyring(t, oldKey))
	before.Create(ctx, &entities.Webhook{
		ID: storetest.ID("wh-1"), TenantID: storetest.TenantA, Name: "probe",
		URL: "https://example.com/hook", Events: []int32{1}, Active: true, Secret: "s",
	})

	after := NewStore(conn, mustKeyring(t, newKey, oldKey)).(interface {
		RotateSecrets(context.Context) (int, error)
	})

	if n, err := after.RotateSecrets(ctx); err != nil || n != 1 {
		t.Fatalf("first pass: %d, %v", n, err)
	}
	// Saying nothing was left is how an operator knows the old key can go.
	if n, err := after.RotateSecrets(ctx); err != nil || n != 0 {
		t.Errorf("second pass: %d, %v; want 0 and no error", n, err)
	}
}

func mustGenerateKey(t *testing.T) string {
	t.Helper()
	k, err := secrets.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

func mustKeyring(t *testing.T, primary string, retired ...string) *secrets.Keyring {
	t.Helper()
	k, err := secrets.NewKeyring(primary, retired...)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return k
}
