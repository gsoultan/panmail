package postgres

import (
	"context"
	"testing"

	"github.com/gsoultan/panmail/internal/storetest"
)

// List builds its query from selectColumns while scanProvider reads every
// column the embedded queries return. When the send ceiling columns were added
// to one and not the other, every list failed on the column count and the
// providers page showed nothing, though each create had succeeded.
func TestListReturnsCreatedProviders(t *testing.T) {
	conn, key := rotationFixture(t)
	ctx := context.Background()
	repo := NewStore(conn, ring(t, key))

	seedProvider(t, repo, "list-1", storetest.TenantA)

	for _, tc := range []struct {
		name, filter, providerType string
	}{
		{name: "unfiltered"},
		{name: "by name", filter: "prim"},
		{name: "by type", providerType: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := repo.List(ctx, storetest.TenantA, tc.filter, tc.providerType, 10, "")
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(got) != 1 || got[0].ID != storetest.ID("list-1") {
				t.Fatalf("list returned %d providers, want the one created", len(got))
			}
		})
	}

	other, _, err := repo.List(ctx, storetest.TenantB, "", "", 10, "")
	if err != nil {
		t.Fatalf("list other tenant: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("other tenant sees %d providers, want none", len(other))
	}
}
