package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/storetest"
)

// The reset-password command relies on both halves of this: the new hash is
// what sign-in reads back, and a reset aimed at nobody says so instead of
// reporting success.

func TestUpdatePasswordReplacesTheHash(t *testing.T) {
	_, users, conn := newMembershipRepos(t)
	ctx := context.Background()
	tenant := seedTenant(t, conn, "t-pw", "Password tenant")
	u := seedUser(t, users, "u-pw", tenant, "pw@example.com", entities.RoleAdmin)

	if err := users.UpdatePassword(ctx, u.ID, "new-hash"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	got, err := users.GetByEmail(ctx, u.Email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Password != "new-hash" {
		t.Fatalf("password = %q, want new-hash", got.Password)
	}
}

func TestUpdatePasswordReportsAMissingUser(t *testing.T) {
	_, users, _ := newMembershipRepos(t)
	err := users.UpdatePassword(context.Background(), storetest.ID("u-missing"), "new-hash")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}
