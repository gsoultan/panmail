package http

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/inbound/usecases"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type WebhookHandler struct {
	usecase usecases.InboundUsecase
}

func NewWebhookHandler(usecase usecases.InboundUsecase) *WebhookHandler {
	return &WebhookHandler{usecase: usecase}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the inbound payload
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Generic inbound payload
	var payload struct {
		TenantID string            `json:"tenant_id"`
		From     string            `json:"from"`
		To       []string          `json:"to"`
		Subject  string            `json:"subject"`
		HTML     string            `json:"html"`
		Text     string            `json:"text"`
		Headers  map[string]string `json:"headers"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		// Fallback for multipart/form-data (common for inbound parse)
		if err := r.ParseMultipartForm(32 << 20); err == nil {
			payload.From = r.FormValue("from")
			payload.Subject = r.FormValue("subject")
			payload.HTML = r.FormValue("html")
			payload.Text = r.FormValue("text")
			// ... extract more
		}
	}

	email := &panmailv1.InboundEmail{
		Id:        uuid.New().String(),
		TenantId:  payload.TenantID,
		From:      payload.From,
		To:        payload.To,
		Subject:   payload.Subject,
		BodyHtml:  payload.HTML,
		BodyText:  payload.Text,
		Timestamp: timestamppb.New(time.Now()),
		Headers:   payload.Headers,
	}

	if err := h.usecase.Process(r.Context(), email); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}
