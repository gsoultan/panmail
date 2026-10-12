package services

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/system_settings/usecases"
	"github.com/gsoultan/panmail/pkg/secrets"
)

func (s *settingsService) GetDataKey(ctx context.Context, _ *connect.Request[panmailv1.GetDataKeyRequest]) (*connect.Response[panmailv1.GetDataKeyResponse], error) {
	if s.dataKey == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("data key management is not available"))
	}
	status, err := s.dataKey.Status(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.GetDataKeyResponse{DataKey: dataKeyToProto(status)}), nil
}

func (s *settingsService) ChangeDataKey(ctx context.Context, req *connect.Request[panmailv1.ChangeDataKeyRequest]) (*connect.Response[panmailv1.ChangeDataKeyResponse], error) {
	if s.dataKey == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("data key management is not available"))
	}

	change, err := s.dataKey.Change(ctx, req.Msg.Key)
	switch {
	case errors.Is(err, secrets.ErrInvalidKey), errors.Is(err, usecases.ErrDataKeyUnchanged):
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, usecases.ErrDataKeyFromEnvironment):
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	case err != nil:
		slog.ErrorContext(ctx, "data encryption key change failed",
			"actor", actorID(ctx), "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Who changed the key, and onto which one. The key itself is never logged.
	slog.InfoContext(ctx, "data encryption key changed",
		"actor", actorID(ctx), "key_id", change.KeyID,
		"generated", change.Key != "", "rotated", change.Rotated)

	status, err := s.dataKey.Status(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.ChangeDataKeyResponse{
		DataKey:      dataKeyToProto(status),
		GeneratedKey: change.Key,
		Rotated:      int32(change.Rotated),
	}), nil
}

func dataKeyToProto(status usecases.DataKeyStatus) *panmailv1.DataKey {
	return &panmailv1.DataKey{
		KeyId:                status.KeyID,
		ManagedByEnvironment: status.Source == usecases.DataKeyFromEnvironment,
		RetiredKeys:          int32(status.RetiredKeys),
	}
}

func actorID(ctx context.Context) string {
	userID, _ := middlewares.GetUserID(ctx)
	return userID
}
