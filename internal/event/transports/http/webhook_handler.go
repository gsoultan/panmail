package http

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"github.com/gsoultan/panmail/internal/event/usecases"
)

type WebhookHandler struct {
	processEventUsecase usecases.ProcessEventUsecase
	providerRepo        providerStores.Repository
}

func NewWebhookHandler(processEventUsecase usecases.ProcessEventUsecase, providerRepo providerStores.Repository) *WebhookHandler {
	return &WebhookHandler{
		processEventUsecase: processEventUsecase,
		providerRepo:        providerRepo,
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Expected path: /webhooks/{tenant_id}/{provider_id}/{type}
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid webhook URL. Expected /webhooks/{tenant_id}/{provider_id}/{type}", http.StatusBadRequest)
		return
	}

	tenantID := parts[1]
	providerID := parts[2]
	webhookType := strings.ToLower(parts[3])

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}

	switch webhookType {
	case "sendgrid":
		h.handleSendGrid(w, r, tenantID, providerID, body)
	case "mailgun":
		h.handleMailgun(w, r, tenantID, providerID, body)
	default:
		h.handleGeneric(w, r, tenantID, providerID, body)
	}
}

func (h *WebhookHandler) handleSendGrid(w http.ResponseWriter, r *http.Request, tenantID, providerID string, body []byte) {
	var events []struct {
		Email     string `json:"email"`
		Event     string `json:"event"`
		MessageID string `json:"sg_message_id"`
		Reason    string `json:"reason"`
		Response  string `json:"response"`
	}
	if err := json.Unmarshal(body, &events); err != nil {
		http.Error(w, "Invalid SendGrid payload", http.StatusBadRequest)
		return
	}

	for _, e := range events {
		eventType := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED
		switch e.Event {
		case "delivered":
			eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED
		case "open":
			eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED
		case "click":
			eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED
		case "bounce":
			eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED
		case "spamreport":
			eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT
		case "unsubscribe":
			eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED
		}

		if eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
			errMsg := e.Reason
			if errMsg == "" {
				errMsg = e.Response
			}
			_ = h.processEventUsecase.RecordEvent(r.Context(), tenantID, providerID, e.MessageID, eventType, e.Email, errMsg, nil)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) handleMailgun(w http.ResponseWriter, r *http.Request, tenantID, providerID string, body []byte) {
	// Simple Mailgun parser (partial)
	var payload struct {
		EventData struct {
			Event     string `json:"event"`
			Recipient string `json:"recipient"`
			Message   struct {
				Headers struct {
					MessageID string `json:"message-id"`
				} `json:"headers"`
			} `json:"message"`
			DeliveryStatus struct {
				Message     string `json:"message"`
				Description string `json:"description"`
			} `json:"delivery-status"`
		} `json:"event-data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid Mailgun payload", http.StatusBadRequest)
		return
	}

	eventType := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED
	switch payload.EventData.Event {
	case "delivered":
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED
	case "opened":
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED
	case "clicked":
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED
	case "failed":
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED
	case "complained":
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT
	case "unsubscribed":
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED
	}

	if eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
		errMsg := payload.EventData.DeliveryStatus.Description
		if errMsg == "" {
			errMsg = payload.EventData.DeliveryStatus.Message
		}
		_ = h.processEventUsecase.RecordEvent(r.Context(), tenantID, providerID, payload.EventData.Message.Headers.MessageID, eventType, payload.EventData.Recipient, errMsg, nil)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) handleGeneric(w http.ResponseWriter, r *http.Request, tenantID, providerID string, body []byte) {
	var e struct {
		Event     string `json:"event"`
		Recipient string `json:"recipient"`
		MessageID string `json:"message_id"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		http.Error(w, "Invalid generic payload", http.StatusBadRequest)
		return
	}

	eventType := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED
	// Try to map string to enum
	val, ok := panmailv1.EmailEventType_value["EMAIL_EVENT_TYPE_"+strings.ToUpper(e.Event)]
	if ok {
		eventType = panmailv1.EmailEventType(val)
	}

	if eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
		_ = h.processEventUsecase.RecordEvent(r.Context(), tenantID, providerID, e.MessageID, eventType, e.Recipient, e.Error, nil)
	}
	w.WriteHeader(http.StatusOK)
}
