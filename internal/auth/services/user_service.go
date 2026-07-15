package services

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/auth/usecases"
)

type userService struct {
	panmailv1connect.UnimplementedUserServiceHandler
	usecase usecases.UserUsecase
}

func NewUserService(usecase usecases.UserUsecase) panmailv1connect.UserServiceHandler {
	return &userService{usecase: usecase}
}

func (s *userService) CreateUser(ctx context.Context, req *connect.Request[panmailv1.CreateUserRequest]) (*connect.Response[panmailv1.CreateUserResponse], error) {
	// Role escalation protection: Only Super Admin can create Super Admin
	callerRole := middlewares.GetRole(ctx)
	if req.Msg.Role == panmailv1.UserRole_USER_ROLE_SUPER_ADMIN && callerRole != "USER_ROLE_SUPER_ADMIN" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions to create Super Admin"))
	}

	tenantID, ok := ctx.Value(middlewares.TenantIDKey).(string)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	user, err := s.usecase.CreateUser(ctx, tenantID, req.Msg.Email, req.Msg.Password, req.Msg.Name, req.Msg.Role.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.CreateUserResponse{
		User: &panmailv1.User{
			Id:               user.ID,
			Email:            user.Email,
			Name:             user.Name,
			TenantId:         user.TenantID,
			Role:             panmailv1.UserRole(panmailv1.UserRole_value[user.Role]),
			TwoFactorEnabled: user.TwoFactorEnabled,
		},
	}), nil
}

func (s *userService) ListUsers(ctx context.Context, req *connect.Request[panmailv1.ListUsersRequest]) (*connect.Response[panmailv1.ListUsersResponse], error) {
	tenantID, ok := ctx.Value(middlewares.TenantIDKey).(string)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	users, nextPageToken, err := s.usecase.ListUsers(ctx, tenantID, int(req.Msg.PageSize), req.Msg.PageToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var protoUsers []*panmailv1.User
	for _, u := range users {
		protoUsers = append(protoUsers, &panmailv1.User{
			Id:               u.ID,
			Email:            u.Email,
			Name:             u.Name,
			TenantId:         u.TenantID,
			Role:             panmailv1.UserRole(panmailv1.UserRole_value[u.Role]),
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
	if req.Msg.Role == panmailv1.UserRole_USER_ROLE_SUPER_ADMIN && callerRole != "USER_ROLE_SUPER_ADMIN" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions to promote to Super Admin"))
	}

	if err := s.usecase.UpdateUserRole(ctx, req.Msg.Id, req.Msg.Role.String()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.UpdateUserRoleResponse{}), nil
}

func (s *userService) DeleteUser(ctx context.Context, req *connect.Request[panmailv1.DeleteUserRequest]) (*connect.Response[panmailv1.DeleteUserResponse], error) {
	if err := s.usecase.DeleteUser(ctx, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.DeleteUserResponse{}), nil
}

func (s *userService) UpdateUserTwoFactor(ctx context.Context, req *connect.Request[panmailv1.UpdateUserTwoFactorRequest]) (*connect.Response[panmailv1.UpdateUserTwoFactorResponse], error) {
	// Role protection: Admin cannot change Super Admin's 2FA
	callerRole := middlewares.GetRole(ctx)
	if callerRole != "USER_ROLE_SUPER_ADMIN" {
		targetUser, err := s.usecase.GetByID(ctx, req.Msg.Id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if targetUser != nil && targetUser.Role == "USER_ROLE_SUPER_ADMIN" {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions to modify Super Admin settings"))
		}
	}

	if err := s.usecase.UpdateUserTwoFactor(ctx, req.Msg.Id, req.Msg.Enabled); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.UpdateUserTwoFactorResponse{}), nil
}
