package services

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/auth/usecases"
)

type AuthService struct {
	usecase usecases.AuthUsecase
}

func NewAuthService(u usecases.AuthUsecase) *AuthService {
	return &AuthService{usecase: u}
}

func (s *AuthService) SignIn(
	ctx context.Context,
	req *connect.Request[panmailv1.SignInRequest],
) (*connect.Response[panmailv1.SignInResponse], error) {
	user, token, twoFactorRequired, twoFactorSetupRequired, twoFactorSecret, twoFactorQRCodeURL, err := s.usecase.SignIn(ctx, req.Msg.Email, req.Msg.Password)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	res := &panmailv1.SignInResponse{
		Token: token,
		User: &panmailv1.User{
			Id:               user.ID,
			Email:            user.Email,
			Name:             user.Name,
			TenantId:         user.TenantID,
			Role:             panmailv1.UserRole(panmailv1.UserRole_value[user.Role]),
			TwoFactorEnabled: user.TwoFactorEnabled,
		},
		TwoFactorRequired:      twoFactorRequired,
		TwoFactorSetupRequired: twoFactorSetupRequired,
		TwoFactorSecret:        twoFactorSecret,
		TwoFactorQrCodeUrl:     twoFactorQRCodeURL,
	}

	return connect.NewResponse(res), nil
}

func (s *AuthService) SignOut(
	ctx context.Context,
	req *connect.Request[panmailv1.SignOutRequest],
) (*connect.Response[panmailv1.SignOutResponse], error) {
	return connect.NewResponse(&panmailv1.SignOutResponse{}), nil
}

func (s *AuthService) GetCurrentUser(
	ctx context.Context,
	req *connect.Request[panmailv1.GetCurrentUserRequest],
) (*connect.Response[panmailv1.GetCurrentUserResponse], error) {
	userID := ctx.Value(middlewares.UserIDKey)
	if userID == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	user, err := s.usecase.GetCurrentUser(ctx, userID.(string))
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&panmailv1.GetCurrentUserResponse{
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

func (s *AuthService) SetupTwoFactor(
	ctx context.Context,
	req *connect.Request[panmailv1.SetupTwoFactorRequest],
) (*connect.Response[panmailv1.SetupTwoFactorResponse], error) {
	userID := ctx.Value(middlewares.UserIDKey)
	if userID == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	secret, qrCodeURL, err := s.usecase.SetupTwoFactor(ctx, userID.(string))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.SetupTwoFactorResponse{
		Secret:    secret,
		QrCodeUrl: qrCodeURL,
	}), nil
}

func (s *AuthService) VerifyTwoFactor(
	ctx context.Context,
	req *connect.Request[panmailv1.VerifyTwoFactorRequest],
) (*connect.Response[panmailv1.VerifyTwoFactorResponse], error) {
	userID := ""
	if uid := ctx.Value(middlewares.UserIDKey); uid != nil {
		userID = uid.(string)
	}

	token, user, verified, err := s.usecase.VerifyTwoFactor(ctx, userID, req.Msg.Email, req.Msg.Code, req.Msg.Secret)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var protoUser *panmailv1.User
	if user != nil {
		protoUser = &panmailv1.User{
			Id:               user.ID,
			Email:            user.Email,
			Name:             user.Name,
			TenantId:         user.TenantID,
			Role:             panmailv1.UserRole(panmailv1.UserRole_value[user.Role]),
			TwoFactorEnabled: user.TwoFactorEnabled,
		}
	}

	return connect.NewResponse(&panmailv1.VerifyTwoFactorResponse{
		Verified: verified,
		Token:    token,
		User:     protoUser,
	}), nil
}

func (s *AuthService) EnableTwoFactor(
	ctx context.Context,
	req *connect.Request[panmailv1.EnableTwoFactorRequest],
) (*connect.Response[panmailv1.EnableTwoFactorResponse], error) {
	userID := ctx.Value(middlewares.UserIDKey)
	if userID == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	err := s.usecase.EnableTwoFactor(ctx, userID.(string), req.Msg.Code, req.Msg.Secret)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(&panmailv1.EnableTwoFactorResponse{Success: true}), nil
}

func (s *AuthService) DisableTwoFactor(
	ctx context.Context,
	req *connect.Request[panmailv1.DisableTwoFactorRequest],
) (*connect.Response[panmailv1.DisableTwoFactorResponse], error) {
	targetUserID := req.Msg.UserId
	currentUserID := ctx.Value(middlewares.UserIDKey).(string)
	currentUserRole := ctx.Value(middlewares.RoleKey).(string)

	if targetUserID == "" {
		targetUserID = currentUserID
	}

	// Check permissions
	if targetUserID != currentUserID {
		if currentUserRole != "USER_ROLE_SUPER_ADMIN" && currentUserRole != "USER_ROLE_ADMIN" {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
		}
	}

	err := s.usecase.DisableTwoFactor(ctx, targetUserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.DisableTwoFactorResponse{Success: true}), nil
}
