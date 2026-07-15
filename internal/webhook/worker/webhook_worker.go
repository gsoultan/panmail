package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/webhook/usecases"
)

type WebhookWorker struct {
	webhookUsecase usecases.WebhookUsecase
	client         *http.Client
	queue          chan WebhookJob
}

type WebhookJob struct {
	TenantID string
	Event    panmailv1.WebhookTriggerEvent
	Payload  any
}

func NewWebhookWorker(webhookUsecase usecases.WebhookUsecase) *WebhookWorker {
	return &WebhookWorker{
		webhookUsecase: webhookUsecase,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		queue: make(chan WebhookJob, 1000),
	}
}

func (w *WebhookWorker) Start(ctx context.Context) {
	slog.Info("Starting Webhook Worker")
	for {
		select {
		case job := <-w.queue:
			w.processJob(ctx, job)
		case <-ctx.Done():
			slog.Info("Stopping Webhook Worker")
			return
		}
	}
}

func (w *WebhookWorker) Enqueue(tenantID string, event panmailv1.WebhookTriggerEvent, payload any) {
	select {
	case w.queue <- WebhookJob{TenantID: tenantID, Event: event, Payload: payload}:
	default:
		slog.Warn("Webhook queue full, dropping job", "tenant_id", tenantID, "event", event)
	}
}

func (w *WebhookWorker) processJob(ctx context.Context, job WebhookJob) {
	// Use a fresh context for webhook delivery as the original request might be cancelled
	deliveryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	webhooks, err := w.webhookUsecase.ListActiveByEvent(deliveryCtx, job.TenantID, job.Event)
	if err != nil {
		slog.Error("failed to list webhooks for event", "error", err, "event", job.Event)
		return
	}

	if len(webhooks) == 0 {
		return
	}

	payload := map[string]any{
		"event":     job.Event.String(),
		"tenant_id": job.TenantID,
		"timestamp": time.Now().Unix(),
		"data":      job.Payload,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal webhook payload", "error", err)
		return
	}

	for _, wh := range webhooks {
		go w.sendWebhook(wh.URL, body)
	}
}

func (w *WebhookWorker) sendWebhook(url string, body []byte) {
	resp, err := w.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		slog.Error("failed to send webhook", "url", url, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Error("webhook returned error status", "url", url, "status", resp.Status)
	}
}

func MapEmailEventToWebhookTrigger(t panmailv1.EmailEventType) panmailv1.WebhookTriggerEvent {
	switch t {
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_DELIVERED
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_OPENED
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_CLICKED
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED,
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_REJECTED
	default:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_UNSPECIFIED
	}
}
