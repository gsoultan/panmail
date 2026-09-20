package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// snsConfirmTimeout bounds the outbound GET that confirms a subscription. It
// is a request made while answering a request, so it cannot be allowed to hang.
const snsConfirmTimeout = 10 * time.Second

// snsClient returns the client used to confirm subscriptions. The field is
// there so a test can answer without reaching the network.
func (h *WebhookHandler) snsClient() *http.Client {
	if h.snsHTTP != nil {
		return h.snsHTTP
	}
	return &http.Client{Timeout: snsConfirmTimeout}
}

// Postmark and SES event ingestion.
//
// Both senders have existed since the ESP provider types landed, and until now
// neither could deliver an event back: an unrecognised webhook type fell to
// verifyGeneric, which reads X-Panmail-Signature. Neither provider sends that
// header, so every request was refused with 401 before it was ever parsed. A
// send path with no feedback path means a tenant never learns that an address
// is dead.
//
// Verification is gsmail's; parsing is ours. gsmail's Parse*Webhook functions
// return only Bounce and Complaint -- ParseSESWebhook errors outright on a
// Delivery notification -- so adopting them would drop delivery, open and
// click, which both providers do send. The verifiers are the opposite case:
// SNS signature checking involves a canonical string, a fetched signing
// certificate and an allowlist on where that certificate may come from, and
// that is not worth reimplementing.

// verifyPostmark authenticates a Postmark webhook from its Authorization
// header.
//
// Postmark has no signature scheme. It authenticates by letting you embed HTTP
// basic credentials in the webhook URL you give it, so the stored secret holds
// `user:pass`. A secret with no colon is taken as the password alone, which is
// what someone who set only a password would expect; gsmail refuses the case
// where both halves are empty.
func verifyPostmark(secret string, header http.Header) error {
	user, pass, found := strings.Cut(secret, ":")
	if !found {
		user, pass = "", secret
	}

	if err := (gsmail.PostmarkVerifier{Username: user, Password: pass}).Verify(header); err != nil {
		return fmt.Errorf("%w: %s", ErrBadWebhookSig, err)
	}
	return nil
}

// verifySNS authenticates an SES notification, which arrives wrapped in SNS.
//
// The topic is checked here rather than through the verifier's own TopicARNs
// field so that one SNSVerifier can be shared across providers: its signing
// certificate cache lives on the instance, and building a verifier per request
// would refetch the certificate from AWS on every webhook. Reading the topic
// from an as-yet unverified body is safe because it is only ever used to
// *reject*: the field is covered by the signature, so a genuine message always
// carries the topic it was published to.
func (h *WebhookHandler) verifySNS(ctx context.Context, topicARNs string, body []byte) error {
	var envelope struct {
		TopicArn string `json:"TopicArn"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ErrMalformedWebhook
	}
	if !topicAllowed(topicARNs, envelope.TopicArn) {
		return fmt.Errorf("%w: message is from topic %q", ErrBadWebhookSig, envelope.TopicArn)
	}

	if _, err := h.snsVerifier.Verify(ctx, body); err != nil {
		return fmt.Errorf("%w: %s", ErrBadWebhookSig, err)
	}
	return nil
}

// topicAllowed reports whether arn is one of the comma-separated ARNs the
// provider is configured with.
//
// A valid SNS signature only proves a message came from SNS -- anyone may
// create a topic and publish a correctly signed notification to a public
// endpoint. Without this check a stranger's topic could post fabricated
// bounces, so the configured ARN is what makes the signature mean anything.
func topicAllowed(configured, arn string) bool {
	if arn == "" {
		return false
	}
	for _, want := range strings.Split(configured, ",") {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(arn)) == 1 {
			return true
		}
	}
	return false
}

// snsEndpointHost bounds the hosts a subscription confirmation may be sent to.
// SubscribeURL comes out of the request body, and confirming a subscription is
// an outbound GET: without this, a signed message from an allowed topic could
// still point the gateway at an arbitrary address.
var snsEndpointHost = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$`)

func snsSubscribeURLAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && snsEndpointHost.MatchString(u.Hostname())
}

// handleSES processes a verified SNS envelope.
func (h *WebhookHandler) handleSES(w http.ResponseWriter, r *http.Request, tenantID, providerID string, body []byte) {
	var msg gsmail.SNSMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "Invalid SNS payload", http.StatusBadRequest)
		return
	}

	switch msg.Type {
	case "SubscriptionConfirmation":
		// SNS delivers nothing until the subscription is confirmed, so an
		// operator who is never told this sees silence rather than an error.
		if !snsSubscribeURLAllowed(msg.SubscribeURL) {
			slog.Warn("refusing SNS subscription confirmation to a non-SNS URL",
				"tenant_id", tenantID, "provider_id", providerID, "url", msg.SubscribeURL)
			http.Error(w, "SubscribeURL is not an SNS endpoint", http.StatusBadRequest)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, msg.SubscribeURL, nil)
		if err != nil {
			http.Error(w, "Could not confirm subscription", http.StatusBadGateway)
			return
		}
		resp, err := h.snsClient().Do(req)
		if err != nil {
			slog.Error("SNS subscription confirmation failed",
				"error", err, "tenant_id", tenantID, "provider_id", providerID)
			http.Error(w, "Could not confirm subscription", http.StatusBadGateway)
			return
		}
		resp.Body.Close()
		slog.Info("confirmed SNS subscription",
			"tenant_id", tenantID, "provider_id", providerID, "topic", msg.TopicArn)
		w.WriteHeader(http.StatusOK)
		return

	case "UnsubscribeConfirmation":
		// Nothing to do, but it is worth saying out loud: the topic has been
		// torn down and events will stop arriving.
		slog.Warn("SES topic unsubscribed; delivery events will stop",
			"tenant_id", tenantID, "provider_id", providerID, "topic", msg.TopicArn)
		w.WriteHeader(http.StatusOK)
		return
	}

	h.recordSESNotification(r.Context(), tenantID, providerID, []byte(msg.Message))
	w.WriteHeader(http.StatusOK)
}

// sesNotification covers both shapes SES publishes.
//
// Feedback notifications carry `notificationType`; Event Publishing carries
// `eventType` over the same envelope. Reading both means a tenant configured
// either way is understood.
type sesNotification struct {
	NotificationType string `json:"notificationType"`
	EventType        string `json:"eventType"`

	Mail struct {
		MessageID   string   `json:"messageId"`
		Destination []string `json:"destination"`
	} `json:"mail"`

	Bounce *struct {
		BounceType        string `json:"bounceType"`
		BouncedRecipients []struct {
			EmailAddress   string `json:"emailAddress"`
			DiagnosticCode string `json:"diagnosticCode"`
			Status         string `json:"status"`
		} `json:"bouncedRecipients"`
	} `json:"bounce"`

	Complaint *struct {
		ComplainedRecipients []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"complainedRecipients"`
		ComplaintFeedbackType string `json:"complaintFeedbackType"`
	} `json:"complaint"`

	Delivery *struct {
		Recipients []string `json:"recipients"`
	} `json:"delivery"`

	Reject *struct {
		Reason string `json:"reason"`
	} `json:"reject"`

	DeliveryDelay *struct {
		DelayType string `json:"delayType"`
	} `json:"deliveryDelay"`
}

func (h *WebhookHandler) recordSESNotification(ctx context.Context, tenantID, providerID string, payload []byte) {
	var n sesNotification
	if err := json.Unmarshal(payload, &n); err != nil {
		slog.Warn("unparseable SES notification",
			"error", err, "tenant_id", tenantID, "provider_id", providerID)
		return
	}

	kind := n.NotificationType
	if kind == "" {
		kind = n.EventType
	}
	msgID := n.Mail.MessageID

	switch kind {
	case "Bounce":
		if n.Bounce == nil {
			return
		}
		// Permanent is the only one that says the address itself is bad. A
		// transient bounce is a mailbox that was full or a server that was
		// busy, and treating it as fatal discards a reachable recipient.
		eventType := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE
		if n.Bounce.BounceType == "Permanent" {
			eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE
		}
		for _, rcpt := range n.Bounce.BouncedRecipients {
			reason := rcpt.DiagnosticCode
			if reason == "" {
				reason = rcpt.Status
			}
			h.record(ctx, tenantID, providerID, msgID, eventType, rcpt.EmailAddress, reason, nil)
		}

	case "Complaint":
		if n.Complaint == nil {
			return
		}
		for _, rcpt := range n.Complaint.ComplainedRecipients {
			h.record(ctx, tenantID, providerID, msgID,
				panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT,
				rcpt.EmailAddress, n.Complaint.ComplaintFeedbackType, nil)
		}

	case "Delivery":
		recipients := n.Mail.Destination
		if n.Delivery != nil && len(n.Delivery.Recipients) > 0 {
			recipients = n.Delivery.Recipients
		}
		h.recordEach(ctx, tenantID, providerID, msgID,
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, recipients, "")

	case "Send":
		h.recordEach(ctx, tenantID, providerID, msgID,
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, n.Mail.Destination, "")

	case "Reject":
		reason := ""
		if n.Reject != nil {
			reason = n.Reject.Reason
		}
		h.recordEach(ctx, tenantID, providerID, msgID,
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED, n.Mail.Destination, reason)

	case "DeliveryDelay":
		reason := ""
		if n.DeliveryDelay != nil {
			reason = n.DeliveryDelay.DelayType
		}
		h.recordEach(ctx, tenantID, providerID, msgID,
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DEFERRED, n.Mail.Destination, reason)

	case "Open":
		h.recordEach(ctx, tenantID, providerID, msgID,
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED, n.Mail.Destination, "")

	case "Click":
		h.recordEach(ctx, tenantID, providerID, msgID,
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED, n.Mail.Destination, "")
	}
}

// recordEach records one event per recipient of a notification that names no
// recipient of its own.
func (h *WebhookHandler) recordEach(
	ctx context.Context,
	tenantID, providerID, msgID string,
	eventType panmailv1.EmailEventType,
	recipients []string,
	errMsg string,
) {
	for _, rcpt := range recipients {
		h.record(ctx, tenantID, providerID, msgID, eventType, rcpt, errMsg, nil)
	}
}

// handlePostmark processes a verified Postmark webhook. Postmark posts one
// record per request rather than a batch.
func (h *WebhookHandler) handlePostmark(w http.ResponseWriter, r *http.Request, tenantID, providerID string, body []byte) {
	var p struct {
		RecordType      string `json:"RecordType"`
		MessageID       string `json:"MessageID"`
		Email           string `json:"Email"`
		Recipient       string `json:"Recipient"`
		Type            string `json:"Type"`
		Description     string `json:"Description"`
		Details         string `json:"Details"`
		SuppressSending bool   `json:"SuppressSending"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "Invalid Postmark payload", http.StatusBadRequest)
		return
	}

	// Delivery and open records name the address in Recipient; bounce and
	// complaint records use Email.
	recipient := p.Email
	if recipient == "" {
		recipient = p.Recipient
	}

	eventType, errMsg := postmarkEvent(p.RecordType, p.Type, p.Description, p.Details, p.SuppressSending)
	if eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
		h.record(r.Context(), tenantID, providerID, p.MessageID, eventType, recipient, errMsg, nil)
	}

	w.WriteHeader(http.StatusOK)
}

// postmarkEvent maps a Postmark record to an event type and its error text.
func postmarkEvent(recordType, bounceType, description, details string, suppress bool) (panmailv1.EmailEventType, string) {
	switch recordType {
	case "Bounce":
		reason := details
		if reason == "" {
			reason = description
		}
		// Postmark names many bounce types; only HardBounce asserts the
		// address is permanently gone. Everything else -- a full mailbox, a
		// throttled server, a transient DNS failure -- is soft.
		if bounceType == "HardBounce" {
			return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, reason
		}
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE, reason
	case "SpamComplaint":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT, description
	case "Delivery":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, ""
	case "Open":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED, ""
	case "Click":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED, ""
	case "SubscriptionChange":
		// The same record type reports both directions. Only a suppression is
		// an unsubscribe; a reactivation must not be recorded as one.
		if suppress {
			return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED, ""
		}
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED, ""
	default:
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED, ""
	}
}
