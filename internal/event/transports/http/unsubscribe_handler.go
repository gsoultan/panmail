package http

import (
	"log/slog"
	"net/http"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/usecases"
	suppressionusecases "github.com/gsoultan/panmail/internal/suppression/usecases"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// UnsubscribeHandler serves the one-click unsubscribe endpoint that Gmail and
// Yahoo have required from bulk senders since February 2024 (RFC 8058).
//
// Two rules govern the method split, and getting either wrong is worse than not
// implementing the feature at all.
//
// POST must unsubscribe immediately, with no confirmation step. Mailbox
// providers treat a confirmation page as a failure to unsubscribe and count it
// against the sending domain.
//
// GET must not unsubscribe. Link-scanning security software, spam filters and
// mail clients fetch every URL in a message; if GET acted, they would
// unsubscribe recipients who never asked. GET therefore returns a page with a
// button that POSTs, which is also what a human following the link expects.
type UnsubscribeHandler struct {
	suppressions suppressionusecases.ManageSuppressionsUsecase
	events       usecases.ProcessEventUsecase
	signer       *tracking.Signer
}

func NewUnsubscribeHandler(
	suppressions suppressionusecases.ManageSuppressionsUsecase,
	events usecases.ProcessEventUsecase,
	signer *tracking.Signer,
) *UnsubscribeHandler {
	return &UnsubscribeHandler{suppressions: suppressions, events: events, signer: signer}
}

func (h *UnsubscribeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, ok := parseSignedRequest(r, 1)
	if !ok {
		http.Error(w, "Invalid unsubscribe link", http.StatusBadRequest)
		return
	}

	link := tracking.Link{
		Kind:      trackingKindUnsubscribe,
		TenantID:  req.tenantID,
		MessageID: req.messageID,
		Recipient: req.recipient,
	}

	// Signed like every other link the gateway emits, so the endpoint cannot be
	// used to suppress an arbitrary address for an arbitrary tenant. Without
	// this, anyone who guessed the URL shape could silently stop a tenant's mail
	// to any recipient.
	if err := h.signer.Verify(link, req.signature); err != nil {
		slog.Warn("rejecting unsigned or altered unsubscribe request",
			"tenant_id", req.tenantID, "message_id", req.messageID, "error", err)
		http.Error(w, "This unsubscribe link is not valid", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.unsubscribe(w, r, req)
	case http.MethodGet, http.MethodHead:
		h.confirmPage(w, req)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *UnsubscribeHandler) unsubscribe(w http.ResponseWriter, r *http.Request, req trackingRequest) {
	if req.recipient == "" {
		http.Error(w, "This unsubscribe link is not valid", http.StatusBadRequest)
		return
	}

	// The address is normalised by the suppression usecase, so the entry
	// created here matches the one the send path looks up.
	_, err := h.suppressions.Add(r.Context(), req.tenantID, &panmailv1.AddSuppressionRequest{
		Email:  req.recipient,
		Reason: "Unsubscribed via one-click",
	})
	if err != nil {
		// A duplicate is success from the recipient's point of view: they asked
		// not to receive mail and they will not. Reporting an error would make
		// the provider record a failed unsubscribe.
		slog.Warn("could not record unsubscribe", "error", err,
			"tenant_id", req.tenantID, "message_id", req.messageID)
	}

	if h.events != nil {
		if err := h.events.RecordEvent(r.Context(), req.tenantID, "", req.messageID,
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED, req.recipient, "", "", nil); err != nil {
			slog.Error("failed to record unsubscribe event", "error", err, "message_id", req.messageID)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(unsubscribedPage))
}

func (h *UnsubscribeHandler) confirmPage(w http.ResponseWriter, req trackingRequest) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Nothing changed, and nothing should be cached: the page reflects a state
	// the next POST will alter.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(confirmPageHTML(req)))
}
