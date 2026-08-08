package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/internal/tenant/entities"
	"github.com/gsoultan/panmail/internal/tenant/repositories"
)

var fixedTime = time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)

// The harness seeds two tenants so the other stores have something to reference,
// and this store lists them, so counts here are relative to that baseline.
const seededTenants = 2

func newRepo(t *testing.T) repositories.TenantRepository {
	t.Helper()
	return NewStore(storetest.NewConnection(t))
}

func tenant(id, name string, retry []string) *entities.Tenant {
	return &entities.Tenant{
		ID:           id,
		Name:         name,
		RetryPattern: retry,
		CreatedAt:    fixedTime,
		UpdatedAt:    fixedTime,
	}
}

// RetryPattern is a []string stored as JSON and is the field that decides how
// long a soft-bounced message keeps being retried, so a silent loss here
// changes delivery behaviour rather than just display.
func TestCreateAndGetRoundTripsTheRetryPattern(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	want := tenant("33333333-3333-3333-3333-333333333333", "Acme", []string{"5m", "1h", "1d"})
	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected the tenant back")
	}
	if got.Name != want.Name {
		t.Errorf("name did not round trip: got %q", got.Name)
	}
	if len(got.RetryPattern) != len(want.RetryPattern) {
		t.Fatalf("retry pattern lost entries: got %v, want %v", got.RetryPattern, want.RetryPattern)
	}
	for i := range want.RetryPattern {
		if got.RetryPattern[i] != want.RetryPattern[i] {
			t.Errorf("retry step %d changed: got %q, want %q", i, got.RetryPattern[i], want.RetryPattern[i])
		}
	}
}

// An empty pattern means "use the default", and must not come back as a
// one-element slice containing an empty string.
func TestEmptyRetryPatternRoundTrips(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	id := "44444444-4444-4444-4444-444444444444"
	if err := repo.Create(ctx, tenant(id, "NoRetry", nil)); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected the tenant back")
	}
	if len(got.RetryPattern) != 0 {
		t.Errorf("expected an empty retry pattern, got %v", got.RetryPattern)
	}
}

func TestUpdateChangesTheStoredRow(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	id := "55555555-5555-5555-5555-555555555555"
	if err := repo.Create(ctx, tenant(id, "Before", []string{"5m"})); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.Update(ctx, tenant(id, "After", []string{"10m", "2h"})); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "After" {
		t.Errorf("name did not update: %q", got.Name)
	}
	if len(got.RetryPattern) != 2 {
		t.Errorf("retry pattern did not update: %v", got.RetryPattern)
	}
}

func TestDeleteRemovesTheTenant(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	id := "66666666-6666-6666-6666-666666666666"
	if err := repo.Create(ctx, tenant(id, "Doomed", nil)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := repo.GetByID(ctx, id)
	if err == nil && got != nil {
		t.Errorf("tenant survived deletion: %+v", got)
	}
}

func TestGetMissingIsNotAnError(t *testing.T) {
	repo := newRepo(t)
	got, err := repo.GetByID(context.Background(), "77777777-7777-7777-7777-777777777777")
	if err == nil && got != nil {
		t.Errorf("expected no tenant, got %+v", got)
	}
}

func TestListPaginationAdvances(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	const created = 4
	for i := range created {
		id := "8888888" + string(rune('a'+i)) + "-8888-8888-8888-888888888888"
		if err := repo.Create(ctx, tenant(id, "T", nil)); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	total := created + seededTenants
	seen := map[string]bool{}
	token := ""
	for page := 0; page < total+2; page++ {
		items, next, err := repo.List(ctx, 2, token)
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("tenant %s returned on more than one page", it.ID)
			}
			seen[it.ID] = true
		}
		if next == "" || len(items) == 0 {
			break
		}
		token = next
	}

	if len(seen) != total {
		t.Errorf("pagination visited %d of %d tenants", len(seen), total)
	}
}
