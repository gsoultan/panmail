package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/auth/usecases"
)

type apiKeyService struct {
	panmailv1connect.UnimplementedApiKeyServiceHandler
	usecase usecases.ApiKeyUsecase
}

func NewApiKeyService(usecase usecases.ApiKeyUsecase) panmailv1connect.ApiKeyServiceHandler {
	return &apiKeyService{usecase: usecase}
}

func (s *apiKeyService) CreateApiKey(ctx context.Context, req *connect.Request[panmailv1.CreateApiKeyRequest]) (*connect.Response[panmailv1.CreateApiKeyResponse], error) {
	tenantID, ok := ctx.Value(middlewares.TenantIDKey).(string)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	var expiresAt *time.Time
	if req.Msg.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.Msg.ExpiresAt)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid expires_at: %w", err))
		}
		expiresAt = &t
	}

	apiKey, plainKey, err := s.usecase.CreateApiKey(ctx, usecases.NewApiKey{
		TenantID:  tenantID,
		Name:      req.Msg.Name,
		Scopes:    toEntityScopes(req.Msg.Scopes),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.CreateApiKeyResponse{
		ApiKey:       toProtoApiKey(apiKey),
		PlainTextKey: plainKey,
	}), nil
}

func (s *apiKeyService) ListApiKeys(ctx context.Context, req *connect.Request[panmailv1.ListApiKeysRequest]) (*connect.Response[panmailv1.ListApiKeysResponse], error) {
	tenantID, ok := ctx.Value(middlewares.TenantIDKey).(string)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	keys, nextPageToken, err := s.usecase.ListApiKeys(ctx, tenantID, int(req.Msg.PageSize), req.Msg.PageToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoKeys := make([]*panmailv1.ApiKey, 0, len(keys))
	for _, k := range keys {
		protoKeys = append(protoKeys, toProtoApiKey(k))
	}

	return connect.NewResponse(&panmailv1.ListApiKeysResponse{
		ApiKeys:       protoKeys,
		NextPageToken: nextPageToken,
	}), nil
}

func (s *apiKeyService) DeleteApiKey(ctx context.Context, req *connect.Request[panmailv1.DeleteApiKeyRequest]) (*connect.Response[panmailv1.DeleteApiKeyResponse], error) {
	tenantID, ok := ctx.Value(middlewares.TenantIDKey).(string)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	if err := s.usecase.DeleteApiKey(ctx, req.Msg.Id, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.DeleteApiKeyResponse{}), nil
}

func (s *apiKeyService) DisableApiKey(ctx context.Context, req *connect.Request[panmailv1.DisableApiKeyRequest]) (*connect.Response[panmailv1.DisableApiKeyResponse], error) {
	tenantID, ok := ctx.Value(middlewares.TenantIDKey).(string)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	if err := s.usecase.DisableApiKey(ctx, req.Msg.Id, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.DisableApiKeyResponse{}), nil
}

func (s *apiKeyService) EnableApiKey(ctx context.Context, req *connect.Request[panmailv1.EnableApiKeyRequest]) (*connect.Response[panmailv1.EnableApiKeyResponse], error) {
	tenantID, ok := ctx.Value(middlewares.TenantIDKey).(string)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	if err := s.usecase.EnableApiKey(ctx, req.Msg.Id, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&panmailv1.EnableApiKeyResponse{}), nil
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func toProtoApiKey(k *entities.ApiKey) *panmailv1.ApiKey {
	if k == nil {
		return nil
	}

	scopes := make([]string, 0, len(k.Scopes))
	for _, s := range k.Scopes {
		scopes = append(scopes, string(s))
	}

	return &panmailv1.ApiKey{
		Id:         k.ID,
		Name:       k.Name,
		Prefix:     k.Prefix,
		CreatedAt:  k.CreatedAt.Format(time.RFC3339),
		LastUsedAt: formatTime(k.LastUsedAt),
		ExpiresAt:  formatTime(k.ExpiresAt),
		IsEnabled:  k.IsEnabled,
		Scopes:     scopes,
	}
}

func toEntityScopes(scopes []string) []entities.Scope {
	out := make([]entities.Scope, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, entities.Scope(s))
	}
	return out
}
