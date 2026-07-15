package usecases

import (
	"context"
	"time"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type WebhookUsecase interface {
	Create(ctx context.Context, tenantID string, req *panmailv1.CreateWebhookRequest) (*panmailv1.Webhook, error)
	List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.Webhook, string, error)
	Delete(ctx context.Context, tenantID, id string) error
	Update(ctx context.Context, tenantID string, req *panmailv1.UpdateWebhookRequest) (*panmailv1.Webhook, error)
	ListActiveByEvent(ctx context.Context, tenantID string, event panmailv1.WebhookTriggerEvent) ([]*entities.Webhook, error)
}

type webhookUsecase struct {
	repo stores.WebhookRepository
}

func NewWebhookUsecase(repo stores.WebhookRepository) WebhookUsecase {
	return &webhookUsecase{repo: repo}
}

func (u *webhookUsecase) Create(ctx context.Context, tenantID string, req *panmailv1.CreateWebhookRequest) (*panmailv1.Webhook, error) {
	events := make([]int32, len(req.Events))
	for i, e := range req.Events {
		events[i] = int32(e)
	}

	w := &entities.Webhook{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Name:      req.Name,
		URL:       req.Url,
		Events:    events,
		Active:    true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := u.repo.Create(ctx, w); err != nil {
		return nil, err
	}

	return u.toProto(w), nil
}

func (u *webhookUsecase) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.Webhook, string, error) {
	webhooks, nextPageToken, err := u.repo.List(ctx, tenantID, pageSize, pageToken)
	if err != nil {
		return nil, "", err
	}

	res := make([]*panmailv1.Webhook, len(webhooks))
	for i, w := range webhooks {
		res[i] = u.toProto(w)
	}
	return res, nextPageToken, nil
}

func (u *webhookUsecase) Delete(ctx context.Context, tenantID, id string) error {
	return u.repo.Delete(ctx, tenantID, id)
}

func (u *webhookUsecase) Update(ctx context.Context, tenantID string, req *panmailv1.UpdateWebhookRequest) (*panmailv1.Webhook, error) {
	events := make([]int32, len(req.Events))
	for i, e := range req.Events {
		events[i] = int32(e)
	}

	w := &entities.Webhook{
		ID:        req.Id,
		TenantID:  tenantID,
		Name:      req.Name,
		URL:       req.Url,
		Events:    events,
		Active:    req.Active,
		UpdatedAt: time.Now(),
	}

	if err := u.repo.Update(ctx, w); err != nil {
		return nil, err
	}

	// Fetch full object to return
	full, err := u.repo.GetByID(ctx, tenantID, req.Id)
	if err != nil {
		return nil, err
	}

	return u.toProto(full), nil
}

func (u *webhookUsecase) ListActiveByEvent(ctx context.Context, tenantID string, event panmailv1.WebhookTriggerEvent) ([]*entities.Webhook, error) {
	return u.repo.ListActiveByEvent(ctx, tenantID, int32(event))
}

func (u *webhookUsecase) toProto(w *entities.Webhook) *panmailv1.Webhook {
	events := make([]panmailv1.WebhookTriggerEvent, len(w.Events))
	for i, e := range w.Events {
		events[i] = panmailv1.WebhookTriggerEvent(e)
	}

	return &panmailv1.Webhook{
		Id:        w.ID,
		TenantId:  w.TenantID,
		Name:      w.Name,
		Url:       w.URL,
		Events:    events,
		Active:    w.Active,
		CreatedAt: timestamppb.New(w.CreatedAt),
	}
}
