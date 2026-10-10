package services

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/tenant/usecases"
)

type deletingUsecase struct {
	usecases.TenantUsecase
	err error
}

func (u deletingUsecase) DeleteTenant(ctx context.Context, id string) error { return u.err }

// A tenant that still has data is a precondition the caller can fix, not a
// server fault; reported as Internal it read as a bug.
func TestDeleteTenantRefusalKeepsItsMeaning(t *testing.T) {
	tests := []struct {
		err  error
		want connect.Code
	}{
		{&usecases.NotEmptyError{Remaining: []string{"2 users"}}, connect.CodeFailedPrecondition},
		{errors.New("database is down"), connect.CodeInternal},
	}
	for _, tc := range tests {
		svc := NewTenantService(deletingUsecase{err: tc.err})
		_, err := svc.DeleteTenant(context.Background(), connect.NewRequest(&panmailv1.DeleteTenantRequest{Id: "t1"}))
		if got := connect.CodeOf(err); got != tc.want {
			t.Errorf("%v mapped to %v, want %v", tc.err, got, tc.want)
		}
	}
}
