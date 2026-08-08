package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"github.com/gsoultan/panmail/internal/event/usecases"
)

// maxWebhookBody caps how much a provider may post. Reading an unbounded body
// into memory is a denial of service anyone on the internet can trigger.
const maxWebhookBody = 1 << 20 // 1 MiB

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
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid webhook URL. Expected /webhooks/{tenant_id}/{provider_id}/{type}", http.StatusBadRequest)
		return
	}

	tenantID, providerID, webhookType := parts[1], parts[2], strings.ToLower(parts[3])

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "Request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}

	// Verify before parsing. Without this anyone who can guess a tenant and
	// provider id can post fabricated bounces and complaints, which drive
	// suppression and fire the tenant's own outbound webhooks.
	if err := h.verify(r, tenantID, providerID, webhookType, body); err != nil {
		slog.Warn("rejecting unverified provider webhook",
			"tenant_id", tenantID, "provider_id", providerID, "type", webhookType, "error", err)
		http.Error(w, "Webhook signature verification failed", http.StatusUnauthorized)
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

// verify checks the request against the secret configured for the provider.
func (h *WebhookHandler) verify(r *http.Request, tenantID, providerID, webhookType string, body []byte) error {
	secret, err := h.webhookSecret(r.Context(), tenantID, providerID)
	if err != nil {
		return err
	}

	switch webhookType {
	case "sendgrid":
		return verifySendGrid(secret,
			r.Header.Get("X-Twilio-Email-Event-Webhook-Signature"),
			r.Header.Get("X-Twilio-Email-Event-Webhook-Timestamp"),
			body)
	case "mailgun":
		return h.verifyMailgunPayload(secret, body)
	default:
		return verifyGeneric(secret, r.Header.Get("X-Panmail-Signature"), body)
	}
}

// verifyMailgunPayload pulls the signature block out of the posted JSON, which
// is where Mailgun puts it for HTTP webhooks.
func (h *WebhookHandler) verifyMailgunPayload(signingKey string, body []byte) error {
	var payload struct {
		Signature struct {
			Timestamp string `json:"timestamp"`
			Token     string `json:"token"`
			Signature string `json:"signature"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ErrMalformedWebhook
	}

	return verifyMailgun(signingKey,
		payload.Signature.Timestamp, payload.Signature.Token, payload.Signature.Signature)
}

func (h *WebhookHandler) webhookSecret(ctx context.Context, tenantID, providerID string) (string, error) {
	provider, err := h.providerRepo.GetByID(ctx, tenantID, providerID)
	if err != nil || provider == nil {
		// Do not distinguish "unknown provider" from "no secret": both are a
		// refusal, and telling them apart enumerates provider ids.
		return "", ErrNoWebhookSecret
	}
	if provider.WebhookSecret == "" {
		return "", ErrNoWebhookSecret
	}
	return provider.WebhookSecret, nil
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
		eventType := sendGridEventType(e.Event)
		if eventType == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
			continue
		}

		errMsg := e.Reason
		if errMsg == "" {
			errMsg = e.Response
		}
		h.record(r.Context(), tenantID, providerID, e.MessageID, eventType, e.Email, errMsg, nil)
	}

	w.WriteHeader(http.StatusOK)
}

func sendGridEventType(event string) panmailv1.EmailEventType {
	switch event {
	case "delivered":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED
	case "open":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED
	case "click":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED
	case "bounce":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED
	case "spamreport":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT
	case "unsubscribe":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED
	default:
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED
	}
}

func (h *WebhookHandler) handleMailgun(w http.ResponseWriter, r *http.Request, tenantID, providerID string, body []byte) {
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

	eventType := mailgunEventType(payload.EventData.Event)
	if eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
		errMsg := payload.EventData.DeliveryStatus.Description
		if errMsg == "" {
			errMsg = payload.EventData.DeliveryStatus.Message
		}
		h.record(r.Context(), tenantID, providerID,
			payload.EventData.Message.Headers.MessageID, eventType,
			payload.EventData.Recipient, errMsg, nil)
	}

	w.WriteHeader(http.StatusOK)
}

func mailgunEventType(event string) panmailv1.EmailEventType {
	switch event {
	case "delivered":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED
	case "opened":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED
	case "clicked":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED
	case "failed":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED
	case "complained":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT
	case "unsubscribed":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED
	default:
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED
	}
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

	if val, ok := panmailv1.EmailEventType_value["EMAIL_EVENT_TYPE_"+strings.ToUpper(e.Event)]; ok {
		eventType := panmailv1.EmailEventType(val)
		if eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
			h.record(r.Context(), tenantID, providerID, e.MessageID, eventType, e.Recipient, e.Error, nil)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) record(
	ctx context.Context,
	tenantID, providerID, messageID string,
	eventType panmailv1.EmailEventType,
	recipient, errMsg string,
	metadata map[string]any,
) {
	if err := h.processEventUsecase.RecordEvent(ctx, tenantID, providerID, messageID, eventType, recipient, "", errMsg, metadata); err != nil {
		slog.Error("failed to record provider webhook event",
			"error", err, "tenant_id", tenantID, "message_id", messageID, "type", eventType.String())
	}
}

// IsVerificationError reports whether err came from signature checking.
func IsVerificationError(err error) bool {
	return errors.Is(err, ErrNoWebhookSecret) ||
		errors.Is(err, ErrBadWebhookSig) ||
		errors.Is(err, ErrStaleWebhook) ||
		errors.Is(err, ErrMalformedWebhook)
}
