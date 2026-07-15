package services

import (
	"context"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/setup/usecases"
)

type SetupService struct {
	usecase usecases.SetupUsecase
}

func NewSetupService(u usecases.SetupUsecase) *SetupService {
	return &SetupService{usecase: u}
}

func (s *SetupService) GetSetupStatus(
	ctx context.Context,
	req *connect.Request[panmailv1.GetSetupStatusRequest],
) (*connect.Response[panmailv1.GetSetupStatusResponse], error) {
	isSetup, err := s.usecase.IsSetup(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.GetSetupStatusResponse{
		IsSetup: isSetup,
	}), nil
}

func (s *SetupService) Setup(
	ctx context.Context,
	req *connect.Request[panmailv1.SetupRequest],
) (*connect.Response[panmailv1.SetupResponse], error) {
	err := s.usecase.Setup(
		ctx,
		req.Msg.DbConfig,
		req.Msg.AdminConfig.Email,
		req.Msg.AdminConfig.Password,
		req.Msg.AdminConfig.Name,
		req.Msg.BaseUrl,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.SetupResponse{
		Success: true,
		Message: "Setup completed successfully",
	}), nil
}

func (s *SetupService) TestDatabaseConnection(
	ctx context.Context,
	req *connect.Request[panmailv1.TestDatabaseConnectionRequest],
) (*connect.Response[panmailv1.TestDatabaseConnectionResponse], error) {
	err := s.usecase.TestDatabaseConnection(ctx, req.Msg.DbConfig)
	if err != nil {
		return connect.NewResponse(&panmailv1.TestDatabaseConnectionResponse{
			Success: false,
			Message: err.Error(),
		}), nil
	}
	return connect.NewResponse(&panmailv1.TestDatabaseConnectionResponse{
		Success: true,
		Message: "Database connection successful",
	}), nil
}
