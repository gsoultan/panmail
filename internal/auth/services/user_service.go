package services

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/auth/usecases"
)

type userService struct {
	panmailv1connect.UnimplementedUserServiceHandler
	usecase     usecases.UserUsecase
	memberships usecases.MembershipUsecase
}

func NewUserService(usecase usecases.UserUsecase, memberships usecases.MembershipUsecase) panmailv1connect.UserServiceHandler {
	return &userService{usecase: usecase, memberships: memberships}
}

// callerTenant is the tenant the request is acting in. Every user lookup by id
// is confined to it, so an id learned in one tenant is not a way into another.
func callerTenant(ctx context.Context) (string, error) {
	tenantID, ok := ctx.Value(middlewares.TenantIDKey).(string)
	if !ok || tenantID == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	return tenantID, nil
}

// asConnectError maps the usecase's sentinel errors onto status codes. Without
// it every refusal reaches the client as Internal, and a caller cannot tell
// "you may not" from "the database is down".
func asConnectError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, usecases.ErrUserNotFound), errors.Is(err, usecases.ErrTenantNotFound), errors.Is(err, usecases.ErrNotAMember):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, usecases.ErrRoleNotAssignable):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, usecases.ErrHomeTenantRemoval),
		errors.Is(err, usecases.ErrHomeTenantOnlyDeletion),
		errors.Is(err, usecases.ErrSuperAdminFromGuestTenant):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func toProtoRole(role string) panmailv1.UserRole {
	return panmailv1.UserRole(panmailv1.UserRole_value[role])
}

func toProtoMembership(m *entities.UserTenant) *panmailv1.UserTenant {
	return &panmailv1.UserTenant{
		TenantId:   m.TenantID,
		TenantName: m.TenantName,
		Role:       toProtoRole(m.Role),
		IsHome:     m.IsHome,
		CreatedAt:  m.CreatedAt.Format(time.RFC3339),
	}
}

func (s *userService) CreateUser(ctx context.Context, req *connect.Request[panmailv1.CreateUserRequest]) (*connect.Response[panmailv1.CreateUserResponse], error) {
	// Role escalation protection: Only Super Admin can create Super Admin
	callerRole := middlewares.GetRole(ctx)
	if req.Msg.Role == panmailv1.UserRole_USER_ROLE_SUPER_ADMIN && callerRole != middlewares.RoleSuperAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions to create Super Admin"))
	}

	tenantID, err := callerTenant(ctx)
	if err != nil {
		return nil, err
	}

	user, err := s.usecase.CreateUser(ctx, tenantID, req.Msg.Email, req.Msg.Password, req.Msg.Name, req.Msg.Role.String())
	if err != nil {
		return nil, asConnectError(err)
	}

	return connect.NewResponse(&panmailv1.CreateUserResponse{
		User: &panmailv1.User{
			Id:               user.ID,
			Email:            user.Email,
			Name:             user.Name,
			TenantId:         user.TenantID,
			Role:             toProtoRole(user.Role),
			TwoFactorEnabled: user.TwoFactorEnabled,
		},
	}), nil
}

func (s *userService) ListUsers(ctx context.Context, req *connect.Request[panmailv1.ListUsersRequest]) (*connect.Response[panmailv1.ListUsersResponse], error) {
	tenantID, err := callerTenant(ctx)
	if err != nil {
		return nil, err
	}

	users, nextPageToken, err := s.usecase.ListUsers(ctx, tenantID, int(req.Msg.PageSize), req.Msg.PageToken)
	if err != nil {
		return nil, asConnectError(err)
	}

	var protoUsers []*panmailv1.User
	for _, u := range users {
		protoUsers = append(protoUsers, &panmailv1.User{
			Id:               u.ID,
			Email:            u.Email,
			Name:             u.Name,
			TenantId:         u.TenantID,
			Role:             toProtoRole(u.Role),
			TwoFactorEnabled: u.TwoFactorEnabled,
		})
	}

	return connect.NewResponse(&panmailv1.ListUsersResponse{
		Users:         protoUsers,
		NextPageToken: nextPageToken,
	}), nil
}

func (s *userService) UpdateUserRole(ctx context.Context, req *connect.Request[panmailv1.UpdateUserRoleRequest]) (*connect.Response[panmailv1.UpdateUserRoleResponse], error) {
	// Role escalation protection: Only Super Admin can promote to Super Admin
	callerRole := middlewares.GetRole(ctx)
	if req.Msg.Role == panmailv1.UserRole_USER_ROLE_SUPER_ADMIN && callerRole != middlewares.RoleSuperAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions to promote to Super Admin"))
	}

	tenantID, err := callerTenant(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.usecase.UpdateUserRole(ctx, tenantID, req.Msg.Id, req.Msg.Role.String()); err != nil {
		return nil, asConnectError(err)
	}

	return connect.NewResponse(&panmailv1.UpdateUserRoleResponse{}), nil
}

func (s *userService) DeleteUser(ctx context.Context, req *connect.Request[panmailv1.DeleteUserRequest]) (*connect.Response[panmailv1.DeleteUserResponse], error) {
	tenantID, err := callerTenant(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.usecase.DeleteUser(ctx, tenantID, req.Msg.Id); err != nil {
		return nil, asConnectError(err)
	}

	return connect.NewResponse(&panmailv1.DeleteUserResponse{}), nil
}

func (s *userService) UpdateUserTwoFactor(ctx context.Context, req *connect.Request[panmailv1.UpdateUserTwoFactorRequest]) (*connect.Response[panmailv1.UpdateUserTwoFactorResponse], error) {
	tenantID, err := callerTenant(ctx)
	if err != nil {
		return nil, err
	}

	// Role protection: Admin cannot change Super Admin's 2FA
	callerRole := middlewares.GetRole(ctx)
	if callerRole != middlewares.RoleSuperAdmin {
		targetUser, err := s.usecase.GetInTenant(ctx, tenantID, req.Msg.Id)
		if err != nil {
			return nil, asConnectError(err)
		}
		if targetUser != nil && targetUser.Role == middlewares.RoleSuperAdmin {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions to modify Super Admin settings"))
		}
	}

	if err := s.usecase.UpdateUserTwoFactor(ctx, tenantID, req.Msg.Id, req.Msg.Enabled); err != nil {
		return nil, asConnectError(err)
	}

	return connect.NewResponse(&panmailv1.UpdateUserTwoFactorResponse{}), nil
}

// AssignUserToTenant lends an existing account to another tenant. It is the
// answer to needing the same person in two tenants, which the old shape could
// not express: users.email is unique, so a second account meant a second
// address and a second password.
func (s *userService) AssignUserToTenant(ctx context.Context, req *connect.Request[panmailv1.AssignUserToTenantRequest]) (*connect.Response[panmailv1.AssignUserToTenantResponse], error) {
	if s.memberships == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("membership is not configured"))
	}
	if req.Msg.UserId == "" || req.Msg.TenantId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id and tenant_id are both required"))
	}

	// An unspecified role arrives as viewer rather than as the user's role at
	// home: an account lent to another tenant should not carry its authority
	// across by default.
	role := req.Msg.Role.String()
	if req.Msg.Role == panmailv1.UserRole_USER_ROLE_UNSPECIFIED {
		role = entities.RoleViewer
	}

	membership, err := s.memberships.AssignUserToTenant(ctx, req.Msg.UserId, req.Msg.TenantId, role)
	if err != nil {
		return nil, asConnectError(err)
	}

	return connect.NewResponse(&panmailv1.AssignUserToTenantResponse{
		Membership: toProtoMembership(membership),
	}), nil
}

func (s *userService) RemoveUserFromTenant(ctx context.Context, req *connect.Request[panmailv1.RemoveUserFromTenantRequest]) (*connect.Response[panmailv1.RemoveUserFromTenantResponse], error) {
	if s.memberships == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("membership is not configured"))
	}

	if err := s.memberships.RemoveUserFromTenant(ctx, req.Msg.UserId, req.Msg.TenantId); err != nil {
		return nil, asConnectError(err)
	}

	return connect.NewResponse(&panmailv1.RemoveUserFromTenantResponse{}), nil
}

// ListUserTenants answers for the caller by default.
//
// Naming somebody else takes super admin, not administrator. The answer names
// every tenant that account belongs to, including ones the asker has nothing
// to do with, so letting an administrator ask about a guest in their tenant
// would turn "who is visiting me" into "where else does this person work".
// The check lives here because it depends on the argument, which the policy
// table cannot see.
func (s *userService) ListUserTenants(ctx context.Context, req *connect.Request[panmailv1.ListUserTenantsRequest]) (*connect.Response[panmailv1.ListUserTenantsResponse], error) {
	if s.memberships == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("membership is not configured"))
	}

	callerID, _ := middlewares.GetUserID(ctx)
	targetID := req.Msg.UserId
	if targetID == "" {
		targetID = callerID
	}
	if targetID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	if targetID != callerID && !middlewares.HasRole(ctx, middlewares.RoleSuperAdmin) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions to read another user's tenants"))
	}

	memberships, err := s.memberships.ListUserTenants(ctx, targetID)
	if err != nil {
		return nil, asConnectError(err)
	}

	tenants := make([]*panmailv1.UserTenant, 0, len(memberships))
	for _, m := range memberships {
		tenants = append(tenants, toProtoMembership(m))
	}

	return connect.NewResponse(&panmailv1.ListUserTenantsResponse{Tenants: tenants}), nil
}
