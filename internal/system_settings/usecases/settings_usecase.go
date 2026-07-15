package usecases

import (
	"context"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/config"
)

type SettingsUsecase interface {
	GetSettings(ctx context.Context) (*panmailv1.SystemSettings, error)
	UpdateSettings(ctx context.Context, settings *panmailv1.SystemSettings) (*panmailv1.SystemSettings, error)
}

type settingsUsecase struct{}

func NewSettingsUsecase() SettingsUsecase {
	return &settingsUsecase{}
}

func (u *settingsUsecase) GetSettings(ctx context.Context) (*panmailv1.SystemSettings, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	defaultRetryPattern := []string{"5m", "15m", "30m", "1h", "3h", "6h", "12h", "24h"}

	if cfg == nil {
		return &panmailv1.SystemSettings{
			LogRetentionDays: 14,
			RetryPattern:     defaultRetryPattern,
		}, nil
	}

	retryPattern := cfg.App.RetryPattern
	if len(retryPattern) == 0 {
		retryPattern = defaultRetryPattern
	}

	return &panmailv1.SystemSettings{
		BaseUrl:          cfg.App.BaseURL,
		LogRetentionDays: int32(cfg.App.LogRetentionDays),
		RetryPattern:     retryPattern,
	}, nil
}

func (u *settingsUsecase) UpdateSettings(ctx context.Context, s *panmailv1.SystemSettings) (*panmailv1.SystemSettings, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &config.Config{}
	}

	cfg.App.BaseURL = s.BaseUrl
	cfg.App.LogRetentionDays = int(s.LogRetentionDays)
	cfg.App.RetryPattern = s.RetryPattern

	if err := config.Save(cfg); err != nil {
		return nil, err
	}

	return s, nil
}
