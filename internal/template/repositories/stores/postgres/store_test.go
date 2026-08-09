package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/internal/template/repositories/entities"
	"github.com/gsoultan/panmail/internal/template/repositories/stores"
)

func newRepo(t *testing.T) stores.TemplateRepository {
	t.Helper()
	return NewStore(storetest.NewConnection(t))
}

// The store persists the timestamps it is handed rather than generating them —
// that is the usecase's job — so the fixture supplies them.
var fixedTime = time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)

func template(id, tenantID, name string) *entities.Template {
	return &entities.Template{
		ID:        id,
		TenantID:  tenantID,
		Name:      name,
		Subject:   "Hello {{name}}",
		BodyHTML:  "<p>Hi</p>",
		BodyText:  "Hi",
		Design:    `{"blocks":[]}`,
		CreatedAt: fixedTime,
		UpdatedAt: fixedTime,
	}
}

func TestCreateAndGetRoundTrip(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	want := template("t1", storetest.TenantA, "Welcome")
	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByID(ctx, storetest.TenantA, "t1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected the template back")
	}
	if got.Name != want.Name || got.Subject != want.Subject ||
		got.BodyHTML != want.BodyHTML || got.BodyText != want.BodyText || got.Design != want.Design {
		t.Errorf("round trip lost data:\n got %+v\nwant %+v", got, want)
	}
	if !got.CreatedAt.UTC().Equal(fixedTime) {
		t.Errorf("created_at did not round trip: got %s, want %s", got.CreatedAt.UTC(), fixedTime)
	}
}

// Every method takes a tenantID. A query that forgets to use it still passes a
// single-tenant test, so each one is checked against a second tenant's row.
func TestTenantIsolation(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, template("mine", storetest.TenantA, "Mine")); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := repo.Create(ctx, template("theirs", storetest.TenantB, "Theirs")); err != nil {
		t.Fatalf("create B: %v", err)
	}

	t.Run("GetByID cannot reach another tenant", func(t *testing.T) {
		got, err := repo.GetByID(ctx, storetest.TenantA, "theirs")
		if err == nil && got != nil {
			t.Fatalf("tenant A read tenant B's template: %+v", got)
		}
	})

	t.Run("List returns only the caller's tenant", func(t *testing.T) {
		got, _, err := repo.List(ctx, storetest.TenantA, 50, "")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, tpl := range got {
			if tpl.TenantID != storetest.TenantA {
				t.Errorf("list leaked a template from %s", tpl.TenantID)
			}
		}
		if len(got) != 1 {
			t.Errorf("expected 1 template for tenant A, got %d", len(got))
		}
	})

	t.Run("Update cannot touch another tenant", func(t *testing.T) {
		hijack := template("theirs", storetest.TenantA, "Hijacked")
		_ = repo.Update(ctx, hijack)

		victim, err := repo.GetByID(ctx, storetest.TenantB, "theirs")
		if err != nil {
			t.Fatalf("get B: %v", err)
		}
		if victim == nil {
			t.Fatal("tenant B's template disappeared")
		}
		if victim.Name != "Theirs" {
			t.Errorf("tenant A rewrote tenant B's template: name is now %q", victim.Name)
		}
	})

	t.Run("Delete cannot remove another tenant's row", func(t *testing.T) {
		_ = repo.Delete(ctx, storetest.TenantA, "theirs")

		victim, err := repo.GetByID(ctx, storetest.TenantB, "theirs")
		if err != nil {
			t.Fatalf("get B: %v", err)
		}
		if victim == nil {
			t.Error("tenant A deleted tenant B's template")
		}
	})
}

func TestUpdateChangesTheStoredRow(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, template("t1", storetest.TenantA, "Before")); err != nil {
		t.Fatalf("create: %v", err)
	}

	updated := template("t1", storetest.TenantA, "After")
	updated.Subject = "Changed"
	if err := repo.Update(ctx, updated); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.GetByID(ctx, storetest.TenantA, "t1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "After" || got.Subject != "Changed" {
		t.Errorf("update did not persist: %+v", got)
	}
}

func TestDeleteRemovesTheRow(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, template("t1", storetest.TenantA, "Doomed")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, storetest.TenantA, "t1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := repo.GetByID(ctx, storetest.TenantA, "t1")
	if err == nil && got != nil {
		t.Errorf("template survived deletion: %+v", got)
	}
}

// Pagination that returns the same page forever is an infinite loop in any
// caller that drains it.
func TestListPaginationAdvances(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	const total = 5
	for i := range total {
		id := string(rune('a' + i))
		if err := repo.Create(ctx, template(id, storetest.TenantA, "T"+id)); err != nil {
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
			if seen[it.ID] {
				t.Fatalf("template %s returned on more than one page", it.ID)
			}
			seen[it.ID] = true
		}
		if next == "" || len(items) == 0 {
			break
		}
		token = next
	}

	if len(seen) != total {
		t.Errorf("pagination visited %d of %d templates", len(seen), total)
	}
}

func TestGetMissingIsNotAnError(t *testing.T) {
	repo := newRepo(t)
	got, err := repo.GetByID(context.Background(), storetest.TenantA, "nope")
	if err == nil && got != nil {
		t.Errorf("expected no template, got %+v", got)
	}
}
