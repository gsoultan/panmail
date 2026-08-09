package services

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/auth/usecases"
)

const (
	roleSuperAdmin = "USER_ROLE_SUPER_ADMIN"
	roleAdmin      = "USER_ROLE_ADMIN"
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
	result, err := s.usecase.SignIn(ctx, usecases.Credentials{
		Email:    req.Msg.Email,
		Password: req.Msg.Password,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	res := &panmailv1.SignInResponse{
		Token:                  result.Token,
		User:                   toProtoUser(result.User),
		TwoFactorRequired:      result.TwoFactorRequired,
		TwoFactorSetupRequired: result.TwoFactorSetupRequired,
		ChallengeToken:         result.ChallengeToken,
	}
	if result.TwoFactorSetup != nil {
		res.TwoFactorSecret = result.TwoFactorSetup.Secret
		res.TwoFactorQrCodeUrl = result.TwoFactorSetup.QRCodeURL
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
	userID, ok := middlewares.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	user, err := s.usecase.GetCurrentUser(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&panmailv1.GetCurrentUserResponse{
		User: toProtoUser(user),
	}), nil
}

func (s *AuthService) SetupTwoFactor(
	ctx context.Context,
	req *connect.Request[panmailv1.SetupTwoFactorRequest],
) (*connect.Response[panmailv1.SetupTwoFactorResponse], error) {
	userID, ok := middlewares.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	setup, err := s.usecase.SetupTwoFactor(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.SetupTwoFactorResponse{
		Secret:    setup.Secret,
		QrCodeUrl: setup.QRCodeURL,
	}), nil
}

// VerifyTwoFactor completes a sign-in that stopped at the second factor. The
// account is identified only by the challenge token issued by SignIn, so this
// endpoint cannot be pointed at an arbitrary user.
func (s *AuthService) VerifyTwoFactor(
	ctx context.Context,
	req *connect.Request[panmailv1.VerifyTwoFactorRequest],
) (*connect.Response[panmailv1.VerifyTwoFactorResponse], error) {
	if req.Msg.ChallengeToken == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("challenge token is required"))
	}

	result, err := s.usecase.VerifyTwoFactorLogin(ctx, req.Msg.ChallengeToken, req.Msg.Code)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	return connect.NewResponse(&panmailv1.VerifyTwoFactorResponse{
		Verified: true,
		Token:    result.Token,
		User:     toProtoUser(result.User),
	}), nil
}

func (s *AuthService) EnableTwoFactor(
	ctx context.Context,
	req *connect.Request[panmailv1.EnableTwoFactorRequest],
) (*connect.Response[panmailv1.EnableTwoFactorResponse], error) {
	userID, ok := middlewares.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	if err := s.usecase.EnableTwoFactor(ctx, userID, req.Msg.Code); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(&panmailv1.EnableTwoFactorResponse{Success: true}), nil
}

func (s *AuthService) DisableTwoFactor(
	ctx context.Context,
	req *connect.Request[panmailv1.DisableTwoFactorRequest],
) (*connect.Response[panmailv1.DisableTwoFactorResponse], error) {
	currentUserID, ok := middlewares.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	currentUserRole := middlewares.GetRole(ctx)

	targetUserID := req.Msg.UserId
	if targetUserID == "" {
		targetUserID = currentUserID
	}

	if targetUserID != currentUserID && currentUserRole != roleSuperAdmin && currentUserRole != roleAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}

	if err := s.usecase.DisableTwoFactor(ctx, targetUserID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.DisableTwoFactorResponse{Success: true}), nil
}

func toProtoUser(user *entities.User) *panmailv1.User {
	if user == nil {
		return nil
	}
	return &panmailv1.User{
		Id:               user.ID,
		Email:            user.Email,
		Name:             user.Name,
		TenantId:         user.TenantID,
		Role:             panmailv1.UserRole(panmailv1.UserRole_value[user.Role]),
		TwoFactorEnabled: user.TwoFactorEnabled,
	}
}
