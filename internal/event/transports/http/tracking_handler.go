package http

import (
	"encoding/base64"
	"net/http"
	"strings"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/usecases"
)

type TrackingHandler struct {
	usecase usecases.ProcessEventUsecase
}

func NewTrackingHandler(u usecases.ProcessEventUsecase) *TrackingHandler {
	return &TrackingHandler{usecase: u}
}

func (h *TrackingHandler) HandleOpen(w http.ResponseWriter, r *http.Request) {
	// Path: /track/open/{tenant_id}/{message_id}/{recipient_base64}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		h.servePixel(w)
		return
	}

	tenantID := parts[2]
	messageID := parts[3]
	recipient := ""

	if len(parts) >= 5 {
		if decoded, err := base64.RawURLEncoding.DecodeString(parts[4]); err == nil {
			recipient = string(decoded)
		}
	}

	_ = h.usecase.RecordEvent(r.Context(), tenantID, "", messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED, recipient, "", "", nil)

	h.servePixel(w)
}

func (h *TrackingHandler) HandleClick(w http.ResponseWriter, r *http.Request) {
	// Path: /track/click/{tenant_id}/{message_id}/{recipient_base64}?url=...
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid tracking URL", http.StatusBadRequest)
		return
	}

	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "Missing target URL", http.StatusBadRequest)
		return
	}

	tenantID := parts[2]
	messageID := parts[3]
	recipient := ""

	if len(parts) >= 5 {
		if decoded, err := base64.RawURLEncoding.DecodeString(parts[4]); err == nil {
			recipient = string(decoded)
		}
	}

	_ = h.usecase.RecordEvent(r.Context(), tenantID, "", messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED, recipient, "", "", map[string]any{"url": targetURL})

	http.Redirect(w, r, targetURL, http.StatusFound)
}

func (h *TrackingHandler) servePixel(w http.ResponseWriter) {
	pixel, _ := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write(pixel)
}
