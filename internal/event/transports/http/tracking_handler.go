package http

import (
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/usecases"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// transparentPixel is a 1x1 GIF served for every open request, valid or not:
// the response must not tell a prober whether a link was genuine.
var transparentPixel = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00,
	0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00,
	0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00,
	0x00, 0x02, 0x01, 0x44, 0x00, 0x3b,
}

type TrackingHandler struct {
	usecase usecases.ProcessEventUsecase
	signer  *tracking.Signer
}

func NewTrackingHandler(u usecases.ProcessEventUsecase, signer *tracking.Signer) *TrackingHandler {
	return &TrackingHandler{usecase: u, signer: signer}
}

// trackingRequest is the parsed form of a tracking URL.
type trackingRequest struct {
	tenantID  string
	messageID string
	recipient string
	signature string
}

// parse reads /track/{kind}/{tenant_id}/{message_id}/{recipient_base64}.
func parseTrackingRequest(r *http.Request) (trackingRequest, bool) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		return trackingRequest{}, false
	}

	req := trackingRequest{
		tenantID:  parts[2],
		messageID: parts[3],
		signature: r.URL.Query().Get(tracking.SignatureParam),
	}

	if len(parts) >= 5 {
		decoded, err := base64.RawURLEncoding.DecodeString(parts[4])
		if err != nil {
			return trackingRequest{}, false
		}
		req.recipient = string(decoded)
	}

	return req, true
}

func (h *TrackingHandler) HandleOpen(w http.ResponseWriter, r *http.Request) {
	defer h.servePixel(w)

	req, ok := parseTrackingRequest(r)
	if !ok {
		return
	}

	link := tracking.Link{
		Kind:      trackingKindOpen,
		TenantID:  req.tenantID,
		MessageID: req.messageID,
		Recipient: req.recipient,
	}

	if err := h.signer.Verify(link, req.signature); err != nil {
		slog.Warn("rejecting unsigned or altered open tracking request",
			"tenant_id", req.tenantID, "message_id", req.messageID, "error", err)
		return
	}

	if err := h.usecase.RecordEvent(r.Context(), req.tenantID, "", req.messageID,
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED, req.recipient, "", "", nil); err != nil {
		slog.Error("failed to record open event", "error", err, "message_id", req.messageID)
	}
}

func (h *TrackingHandler) HandleClick(w http.ResponseWriter, r *http.Request) {
	req, ok := parseTrackingRequest(r)
	if !ok {
		http.Error(w, "Invalid tracking URL", http.StatusBadRequest)
		return
	}

	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "Missing target URL", http.StatusBadRequest)
		return
	}

	link := tracking.Link{
		Kind:      trackingKindClick,
		TenantID:  req.tenantID,
		MessageID: req.messageID,
		Recipient: req.recipient,
		TargetURL: targetURL,
	}

	// The signature covers the destination, so this endpoint can only forward
	// to a URL this server put in a message. Without that check it is an open
	// redirect on the domain recipients are taught to trust.
	if err := h.signer.Verify(link, req.signature); err != nil {
		slog.Warn("rejecting unsigned or altered click tracking request",
			"tenant_id", req.tenantID, "message_id", req.messageID, "error", err)
		http.Error(w, "Invalid tracking link", http.StatusForbidden)
		return
	}

	// Defence in depth: a signature proves we generated the link, not that the
	// destination is a safe kind of URL.
	if err := tracking.ValidateTarget(targetURL); err != nil {
		http.Error(w, "Unsupported redirect target", http.StatusBadRequest)
		return
	}

	if err := h.usecase.RecordEvent(r.Context(), req.tenantID, "", req.messageID,
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED, req.recipient, "", "",
		map[string]any{"url": targetURL}); err != nil {
		slog.Error("failed to record click event", "error", err, "message_id", req.messageID)
	}

	http.Redirect(w, r, targetURL, http.StatusFound)
}

func (h *TrackingHandler) servePixel(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	if _, err := w.Write(transparentPixel); err != nil {
		slog.Debug("failed to write tracking pixel", "error", err)
	}
}
