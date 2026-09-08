package http

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
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
	usecase  usecases.ProcessEventUsecase
	signer   *tracking.Signer
	messages SentMessages
}

// SentMessages reads back the body a message was built from, before tracking
// injection — which is to say, the exact set of destinations panmail was asked
// to link to.
type SentMessages interface {
	GetMessage(ctx context.Context, tenantID, messageID string) (*entities.EmailMessage, error)
}

// WithSentMessages installs the lookup that lets a link outlive the key that
// signed it. Optional: without it a link whose signature fails is simply
// refused, which is what happened before.
func (h *TrackingHandler) WithSentMessages(m SentMessages) *TrackingHandler {
	h.messages = m
	return h
}

// wasSentInThisMessage reports whether panmail actually put this destination in
// this message.
//
// This is what makes it safe to follow a link whose signature no longer
// verifies. The signature proves panmail minted the link; this proves the same
// thing a different way, from the copy of the message panmail stored when it
// sent it. A caller who forges a signature still cannot choose where the
// redirect goes, because the destination has to be one this tenant already
// emailed in the message whose id they named — so this is not an open redirect,
// which is the whole reason the signature exists.
//
// It fails closed: no lookup wired, no such message, a message whose body
// retention has already removed, or a destination that is not in it, and the
// request is refused exactly as before.
func (h *TrackingHandler) wasSentInThisMessage(ctx context.Context, req trackingRequest, target string) bool {
	if h.messages == nil || req.tenantID == "" || req.messageID == "" {
		return false
	}

	msg, err := h.messages.GetMessage(ctx, req.tenantID, req.messageID)
	if err != nil || msg == nil {
		return false
	}

	for _, sent := range tracking.LinkTargets(msg.BodyHTML) {
		if sent == target {
			return true
		}
	}
	return false
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
// parseTrackingRequest reads /track/{kind}/{tenant}/{message}/{recipient}.
func parseTrackingRequest(r *http.Request) (trackingRequest, bool) {
	return parseSignedRequest(r, 2)
}

// parseSignedRequest reads {tenant}/{message}/{recipient} from a path, after
// skipping prefixSegments leading segments.
//
// The prefix length is a parameter because the endpoints differ: tracking links
// are /track/open/... (two segments) while unsubscribe is /unsubscribe/...
// (one). It used to be hardcoded to two, so a handler mounted on a shorter path
// silently read the message id as the tenant and rejected every valid link.
func parseSignedRequest(r *http.Request, prefixSegments int) (trackingRequest, bool) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// tenant and message are required; recipient is optional.
	if len(parts) < prefixSegments+2 {
		return trackingRequest{}, false
	}

	req := trackingRequest{
		tenantID:  parts[prefixSegments],
		messageID: parts[prefixSegments+1],
		signature: r.URL.Query().Get(tracking.SignatureParam),
	}

	if len(parts) >= prefixSegments+3 {
		decoded, err := base64.RawURLEncoding.DecodeString(parts[prefixSegments+2])
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
	//
	// A signature that does not verify is not always a forgery. It is also what
	// every link in every message already delivered looks like once the key
	// behind it changes — a rotation, a restore that brought the database but
	// not the config, a second instance started from a different one. Those
	// messages are in inboxes and cannot be reissued, so refusing them outright
	// strands real recipients on a 403 for as long as the mail exists.
	//
	// So the stored copy of the message gets to answer the same question the
	// signature does: did panmail put this destination in this message? If it
	// did, the link is ours whatever happened to the key, and the redirect is
	// still confined to a URL this tenant sent.
	if err := h.signer.Verify(link, req.signature); err != nil {
		if !h.wasSentInThisMessage(r.Context(), req, targetURL) {
			slog.Warn("rejecting unsigned or altered click tracking request",
				"tenant_id", req.tenantID, "message_id", req.messageID, "error", err)
			http.Error(w, "Invalid tracking link", http.StatusForbidden)
			return
		}
		slog.Warn("following a click link whose signature no longer verifies, because the stored message still contains this destination",
			"tenant_id", req.tenantID, "message_id", req.messageID, "error", err,
			"detail", "the key that signed this link is not the key in use now; links already delivered cannot be reissued",
			"fix", "if this was not an intentional key rotation, check that every instance shares auth.symmetric_key")
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
