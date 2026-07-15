package services

import (
	"context"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/system_settings/usecases"
)

type settingsService struct {
	usecase usecases.SettingsUsecase
}

func NewSettingsService(usecase usecases.SettingsUsecase) panmailv1connect.SystemSettingsServiceHandler {
	return &settingsService{usecase: usecase}
}

func (s *settingsService) GetSettings(ctx context.Context, req *connect.Request[panmailv1.GetSettingsRequest]) (*connect.Response[panmailv1.GetSettingsResponse], error) {
	settings, err := s.usecase.GetSettings(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.GetSettingsResponse{Settings: settings}), nil
}

func (s *settingsService) UpdateSettings(ctx context.Context, req *connect.Request[panmailv1.UpdateSettingsRequest]) (*connect.Response[panmailv1.UpdateSettingsResponse], error) {
	settings, err := s.usecase.UpdateSettings(ctx, req.Msg.Settings)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.UpdateSettingsResponse{Settings: settings}), nil
}
