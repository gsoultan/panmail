package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	"github.com/gsoultan/panmail/internal/suppression/repositories/stores"
)

var fixedTime = time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)

func newRepo(t *testing.T) stores.SuppressionRepository {
	t.Helper()
	return NewStore(storetest.NewConnection(t))
}

func suppression(id, tenantID, email string) *entities.Suppression {
	// The fixture is named for readability; the column is a UUID on PostgreSQL
	// and merely a VARCHAR on SQLite. Mapping here keeps call sites saying
	// "k1" while the database gets something it will accept — the difference
	// that let these fixtures pass for as long as only SQLite was run.
	id = storetest.ID(id)

	return &entities.Suppression{
		ID:        id,
		TenantID:  tenantID,
		Email:     email,
		Reason:    "hard bounce",
		CreatedAt: fixedTime,
	}
}

func TestCreateAndGetByEmail(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, suppression("s1", storetest.TenantA, "gone@example.com")); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByEmail(ctx, storetest.TenantA, "gone@example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected the suppression back")
	}
	if got.Email != "gone@example.com" || got.Reason != "hard bounce" {
		t.Errorf("round trip lost data: %+v", got)
	}
}

// Suppression decides whether mail is sent at all. A lookup that ignored its
// tenant would let one tenant's bounce silently block another tenant's
// delivery to the same address — the failure would look like mail vanishing.
func TestSuppressionIsScopedToItsTenant(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, suppression("s1", storetest.TenantB, "shared@example.com")); err != nil {
		t.Fatalf("create B: %v", err)
	}

	got, err := repo.GetByEmail(ctx, storetest.TenantA, "shared@example.com")
	if err != nil {
		t.Fatalf("get A: %v", err)
	}
	if got != nil {
		t.Errorf("tenant A saw tenant B's suppression: %+v", got)
	}

	list, _, err := repo.List(ctx, storetest.TenantA, 50, "")
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("tenant A listed %d of tenant B's suppressions", len(list))
	}
}

func TestDeleteIsScopedToItsTenant(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, suppression("s1", storetest.TenantB, "shared@example.com")); err != nil {
		t.Fatalf("create B: %v", err)
	}

	// Tenant A trying to unsuppress an address must not lift tenant B's block.
	_ = repo.Delete(ctx, storetest.TenantA, "shared@example.com")

	got, err := repo.GetByEmail(ctx, storetest.TenantB, "shared@example.com")
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	if got == nil {
		t.Error("tenant A removed tenant B's suppression")
	}
}

func TestDeleteRemovesTheAddress(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, suppression("s1", storetest.TenantA, "gone@example.com")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, storetest.TenantA, "gone@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := repo.GetByEmail(ctx, storetest.TenantA, "gone@example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Errorf("suppression survived deletion: %+v", got)
	}
}

// An address that was never suppressed must read as absent rather than as an
// error, because the send path treats an error as a reason not to send.
func TestUnknownAddressIsAbsentNotAnError(t *testing.T) {
	repo := newRepo(t)
	got, err := repo.GetByEmail(context.Background(), storetest.TenantA, "fine@example.com")
	if err != nil {
		t.Fatalf("an unsuppressed address must not error: %v", err)
	}
	if got != nil {
		t.Errorf("expected no suppression, got %+v", got)
	}
}

func TestListPaginationAdvances(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	const total = 5
	for i := range total {
		id := string(rune('a' + i))
		if err := repo.Create(ctx, suppression(id, storetest.TenantA, id+"@example.com")); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	seen := map[string]bool{}
	token := ""
	for page := 0; page < total+2; page++ {
		items, next, err := repo.List(ctx, storetest.TenantA, 2, token)
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		for _, it := range items {
			if seen[it.Email] {
				t.Fatalf("%s returned on more than one page", it.Email)
			}
			seen[it.Email] = true
		}
		if next == "" || len(items) == 0 {
			break
		}
		token = next
	}

	if len(seen) != total {
		t.Errorf("pagination visited %d of %d suppressions", len(seen), total)
	}
}
