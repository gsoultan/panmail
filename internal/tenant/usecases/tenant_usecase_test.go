package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/panmail/internal/tenant/repositories"
)

type countingRepo struct {
	repositories.TenantRepository // unused methods panic

	deps    repositories.Dependents
	deleted []string
}

func (r *countingRepo) CountDependents(ctx context.Context, id string) (repositories.Dependents, error) {
	return r.deps, nil
}

func (r *countingRepo) Delete(ctx context.Context, id string) error {
	r.deleted = append(r.deleted, id)
	return nil
}

type recordingForgetter struct{ forgot []string }

func (f *recordingForgetter) Forget(tenantID string) { f.forgot = append(f.forgot, tenantID) }

func TestDeleteTenant(t *testing.T) {
	tests := []struct {
		name        string
		deps        repositories.Dependents
		wantRefused bool
		wantInMsg   []string
	}{
		{name: "an empty tenant is deleted"},
		{
			name:        "a tenant with data is refused, naming what is left",
			deps:        repositories.Dependents{Users: 2, EmailProviders: 1, OutboxMessages: 3},
			wantRefused: true,
			wantInMsg:   []string{"2 users", "1 email provider", "3 queued messages"},
		},
		{
			name:        "a guest membership alone still blocks",
			deps:        repositories.Dependents{GuestMembers: 1},
			wantRefused: true,
			wantInMsg:   []string{"1 guest member"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &countingRepo{deps: tc.deps}
			forgetter := &recordingForgetter{}
			u := NewTenantUsecase(repo, forgetter)

			err := u.DeleteTenant(context.Background(), "t1")

			if !tc.wantRefused {
				if err != nil {
					t.Fatalf("delete: %v", err)
				}
				if len(repo.deleted) != 1 {
					t.Errorf("deleted %v, want [t1]", repo.deleted)
				}
				// The send limiter's bucket for a deleted tenant is state
				// nothing will ever read again.
				if len(forgetter.forgot) != 1 || forgetter.forgot[0] != "t1" {
					t.Errorf("forgot %v, want [t1]", forgetter.forgot)
				}
				return
			}

			if !errors.Is(err, ErrTenantNotEmpty) {
				t.Fatalf("err = %v, want ErrTenantNotEmpty", err)
			}
			for _, part := range tc.wantInMsg {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("message %q does not mention %q", err.Error(), part)
				}
			}
			if len(repo.deleted) != 0 {
				t.Error("a refused tenant was deleted anyway")
			}
			if len(forgetter.forgot) != 0 {
				t.Error("a refused tenant's state was forgotten")
			}
		})
	}
}
