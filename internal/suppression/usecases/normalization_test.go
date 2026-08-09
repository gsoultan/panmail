package usecases

import (
	"context"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/storetest"
	postgres "github.com/gsoultan/panmail/internal/suppression/repositories/stores/postgres"
)

// A suppression list that fails open is worse than no list: the operator
// believes bounced addresses are blocked while mail keeps going out to them,
// burning the sending reputation the list exists to protect.
//
// It fails open whenever the same mailbox can produce two different keys, which
// is what happened before addresses were normalised — a bounce recorded for
// "Alice@Example.COM" did not stop a later send to "alice@example.com".
//
// These run against the real store so the check exercises the actual query.

func newUsecase(t *testing.T) ManageSuppressionsUsecase {
	t.Helper()
	return NewManageSuppressionsUsecase(postgres.NewStore(storetest.NewConnection(t)))
}

func TestSuppressionMatchesRegardlessOfSpelling(t *testing.T) {
	spellings := []struct {
		name    string
		stored  string
		checked string
	}{
		{"upper-case local part", "Alice@example.com", "alice@example.com"},
		{"upper-case domain", "alice@EXAMPLE.COM", "alice@example.com"},
		{"mixed case both ways", "ALICE@Example.Com", "aLiCe@eXaMpLe.CoM"},
		{"display name on the stored address", "Alice Smith <alice@example.com>", "alice@example.com"},
		{"display name on the checked address", "alice@example.com", "Alice Smith <Alice@Example.com>"},
		{"surrounding whitespace", "  alice@example.com  ", "alice@example.com"},
	}

	for _, tc := range spellings {
		t.Run(tc.name, func(t *testing.T) {
			u := newUsecase(t)
			ctx := context.Background()

			if _, err := u.Add(ctx, storetest.TenantA, &panmailv1.AddSuppressionRequest{
				Email:  tc.stored,
				Reason: "hard bounce",
			}); err != nil {
				t.Fatalf("add %q: %v", tc.stored, err)
			}

			suppressed, reason, err := u.Check(ctx, storetest.TenantA, tc.checked)
			if err != nil {
				t.Fatalf("check %q: %v", tc.checked, err)
			}
			if !suppressed {
				t.Errorf("stored %q but %q was not suppressed", tc.stored, tc.checked)
			}
			if reason != "hard bounce" {
				t.Errorf("reason lost: %q", reason)
			}
		})
	}
}

// Unsubscribing has to work whichever spelling the recipient's client sends,
// or the address stays blocked and the operator cannot clear it.
func TestRemoveMatchesRegardlessOfSpelling(t *testing.T) {
	u := newUsecase(t)
	ctx := context.Background()

	if _, err := u.Add(ctx, storetest.TenantA, &panmailv1.AddSuppressionRequest{
		Email: "Bob@Example.COM", Reason: "unsubscribed",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := u.Remove(ctx, storetest.TenantA, "bob@example.com"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	suppressed, _, err := u.Check(ctx, storetest.TenantA, "Bob@Example.COM")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if suppressed {
		t.Error("the address is still suppressed after being removed under a different spelling")
	}
}

// Normalising must not merge distinct mailboxes. The local part is
// case-insensitive in practice but two different local parts are two mailboxes.
func TestDistinctMailboxesStayDistinct(t *testing.T) {
	u := newUsecase(t)
	ctx := context.Background()

	if _, err := u.Add(ctx, storetest.TenantA, &panmailv1.AddSuppressionRequest{
		Email: "alice@example.com", Reason: "hard bounce",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	for _, other := range []string{"alice@example.org", "alicia@example.com", "alice+tag@example.com"} {
		suppressed, _, err := u.Check(ctx, storetest.TenantA, other)
		if err != nil {
			t.Fatalf("check %q: %v", other, err)
		}
		if suppressed {
			t.Errorf("%q was wrongly treated as the same mailbox as alice@example.com", other)
		}
	}
}

// Suppression is per tenant, and normalising must not weaken that.
func TestNormalisationDoesNotCrossTenants(t *testing.T) {
	u := newUsecase(t)
	ctx := context.Background()

	if _, err := u.Add(ctx, storetest.TenantB, &panmailv1.AddSuppressionRequest{
		Email: "Shared@Example.com", Reason: "hard bounce",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	suppressed, _, err := u.Check(ctx, storetest.TenantA, "shared@example.com")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if suppressed {
		t.Error("tenant A saw tenant B's suppression")
	}
}
