package services

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/auth/usecases"
)

// Who may ask about somebody else's memberships. The answer names every tenant
// an account belongs to, so it crosses the boundary the rest of the system
// spends its effort maintaining.

type stubMemberships struct {
	askedAbout []string
}

func (s *stubMemberships) AssignUserToTenant(ctx context.Context, userID, tenantID, role string) (*entities.UserTenant, error) {
	return &entities.UserTenant{UserID: userID, TenantID: tenantID, Role: role}, nil
}
func (s *stubMemberships) RemoveUserFromTenant(ctx context.Context, userID, tenantID string) error {
	return nil
}
func (s *stubMemberships) ListUserTenants(ctx context.Context, userID string) ([]*entities.UserTenant, error) {
	s.askedAbout = append(s.askedAbout, userID)
	return []*entities.UserTenant{{UserID: userID, TenantID: "t1", TenantName: "Acme", Role: entities.RoleViewer}}, nil
}
func (s *stubMemberships) RoleIn(ctx context.Context, userID, tenantID string) (string, error) {
	return "", nil
}

func callerContext(userID, role string) context.Context {
	return middlewares.WithPrincipal(context.Background(), &middlewares.Principal{
		Kind:     middlewares.PrincipalUser,
		UserID:   userID,
		TenantID: "t1",
		Role:     role,
	})
}

func listTenants(t *testing.T, ctx context.Context, memberships usecases.MembershipUsecase, targetID string) (*panmailv1.ListUserTenantsResponse, error) {
	t.Helper()
	svc := NewUserService(nil, memberships)
	res, err := svc.ListUserTenants(ctx, connect.NewRequest(&panmailv1.ListUserTenantsRequest{UserId: targetID}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// The console populates its own switcher this way, so it must work without any
// special standing.
func TestListUserTenantsAnswersForTheCaller(t *testing.T) {
	memberships := &stubMemberships{}

	got, err := listTenants(t, callerContext("u1", entities.RoleViewer), memberships, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got.Tenants) != 1 || got.Tenants[0].TenantName != "Acme" {
		t.Fatalf("got %+v, want the caller's own membership", got.Tenants)
	}
	if len(memberships.askedAbout) != 1 || memberships.askedAbout[0] != "u1" {
		t.Errorf("asked about %v, want the caller themselves", memberships.askedAbout)
	}
}

// An administrator sees guests in their own tenant, so they know those ids.
// Answering for them would turn "who is visiting me" into "where else does
// this person work".
func TestListUserTenantsRefusesAnAdministratorAskingAboutSomebodyElse(t *testing.T) {
	memberships := &stubMemberships{}

	_, err := listTenants(t, callerContext("admin", entities.RoleAdmin), memberships, "someone-else")
	if err == nil {
		t.Fatal("an administrator read another account's tenants")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", got)
	}
	if len(memberships.askedAbout) != 0 {
		t.Errorf("the usecase was reached anyway, for %v", memberships.askedAbout)
	}
}

// A super admin already sees every tenant, so nothing is disclosed that they
// could not reach directly.
func TestListUserTenantsAllowsASuperAdminAskingAboutSomebodyElse(t *testing.T) {
	memberships := &stubMemberships{}

	got, err := listTenants(t, callerContext("root", entities.RoleSuperAdmin), memberships, "someone-else")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got.Tenants) != 1 {
		t.Fatalf("got %+v, want the target's membership", got.Tenants)
	}
	if len(memberships.askedAbout) != 1 || memberships.askedAbout[0] != "someone-else" {
		t.Errorf("asked about %v, want the named user", memberships.askedAbout)
	}
}

// An unspecified role must not arrive as whatever the account holds at home:
// lending an account to a tenant should not carry its authority across.
func TestAssignDefaultsToViewerWhenNoRoleIsGiven(t *testing.T) {
	svc := NewUserService(nil, &stubMemberships{})

	res, err := svc.AssignUserToTenant(
		callerContext("root", entities.RoleSuperAdmin),
		connect.NewRequest(&panmailv1.AssignUserToTenantRequest{UserId: "u1", TenantId: "t2"}),
	)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if res.Msg.Membership.Role != panmailv1.UserRole_USER_ROLE_VIEWER {
		t.Errorf("role = %v, want viewer", res.Msg.Membership.Role)
	}
}

func TestAssignRequiresBothIds(t *testing.T) {
	svc := NewUserService(nil, &stubMemberships{})
	ctx := callerContext("root", entities.RoleSuperAdmin)

	for _, req := range []*panmailv1.AssignUserToTenantRequest{
		{UserId: "", TenantId: "t2"},
		{UserId: "u1", TenantId: ""},
	} {
		_, err := svc.AssignUserToTenant(ctx, connect.NewRequest(req))
		if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
			t.Errorf("%+v: code = %v, want InvalidArgument", req, got)
		}
	}
}

// The usecase's refusals have to reach the client as something it can act on.
// Reported as Internal, "that is the user's home tenant" reads as a bug.
func TestUsecaseRefusalsKeepTheirMeaning(t *testing.T) {
	tests := []struct {
		err  error
		want connect.Code
	}{
		{usecases.ErrUserNotFound, connect.CodeNotFound},
		{usecases.ErrTenantNotFound, connect.CodeNotFound},
		{usecases.ErrNotAMember, connect.CodeNotFound},
		{usecases.ErrRoleNotAssignable, connect.CodeInvalidArgument},
		{usecases.ErrHomeTenantRemoval, connect.CodeFailedPrecondition},
		{usecases.ErrHomeTenantOnlyDeletion, connect.CodeFailedPrecondition},
		{usecases.ErrSuperAdminFromGuestTenant, connect.CodeFailedPrecondition},
		{errors.New("database is down"), connect.CodeInternal},
	}

	for _, tc := range tests {
		if got := connect.CodeOf(asConnectError(tc.err)); got != tc.want {
			t.Errorf("%v mapped to %v, want %v", tc.err, got, tc.want)
		}
	}
}
