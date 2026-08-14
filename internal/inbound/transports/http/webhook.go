package http

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/inbound/usecases"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// maxInboundBody caps an inbound message payload. Generous enough for a mail
// body with inline content, small enough that a flood cannot exhaust memory.
const maxInboundBody = 25 << 20 // 25 MiB

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

	// Parse the inbound payload. The body is capped: reading an unbounded
	// request into memory on a public endpoint is a denial of service anyone
	// can trigger.
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInboundBody))
	if err != nil {
		http.Error(w, "Request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}

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
		// Logged, not returned. This endpoint is public and unauthenticated —
		// it has to be, since the provider posting to it cannot hold a
		// credential — and Process writes to the inbound store, the outbox and
		// any webhook subscriptions, so its error can carry filesystem paths,
		// the database user and host, or an internal endpoint's address. The
		// provider on the other end needs a status code to decide whether to
		// retry; it has no use for any of that.
		slog.Error("failed to process an inbound email",
			"error", err, "tenant_id", email.TenantId, "id", email.Id)
		http.Error(w, "Could not process the message", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}
