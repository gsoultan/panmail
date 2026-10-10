package http

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gsoultan/gsmail"
	smtpmime "github.com/gsoultan/panmail/internal/email/transports/smtp/mime"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
)

// SES receives mail for a tenant through a receipt rule, and a rule's SNS
// action publishes each message to a topic that posts here. Nothing in that
// chain can carry a shared secret: SNS authenticates by signing every message
// with an AWS certificate. So a post from an SES provider is authenticated by
// that signature, and by the topic it names being one the provider is
// configured with -- a valid signature only proves a message came from SNS,
// and anyone can create a topic and point it at a public URL.

// snsTimeout bounds each outbound request SNS handling makes, the certificate
// fetch and the subscription confirmation. Both happen while a post waits.
const snsTimeout = 10 * time.Second

// newSNSClient returns the client for those requests. It does not follow
// redirects: the host each request goes to has been checked, and a redirect
// would send it somewhere that has not.
func newSNSClient() *http.Client {
	return &http.Client{
		Timeout: snsTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// snsHost is an SNS endpoint, and its region. Both URLs SNS puts in a message
// -- the signing certificate's and the subscription confirmation's -- must be
// on one, and in the topic's own region.
var snsHost = regexp.MustCompile(`^sns\.([a-z0-9-]+)\.amazonaws\.com(?:\.cn)?$`)

// snsURLAllowed reports whether raw is an https URL on the SNS endpoint of
// region, with no port and no credentials.
//
// The URLs come out of the request body. The certificate URL is fetched
// before the signature can be checked -- the certificate is what checks it --
// so it is attacker-controlled at that point. Pinning it to SNS in the
// topic's region means a forged message can make panmail fetch nothing but an
// AWS certificate.
func snsURLAllowed(raw, region string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	m := snsHost.FindStringSubmatch(u.Host)
	return m != nil && m[1] == region
}

// topicRegion returns the region of an SNS topic ARN,
// arn:<partition>:sns:<region>:<account>:<name>, or "".
func topicRegion(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) != 6 || parts[0] != "arn" || parts[2] != "sns" {
		return ""
	}
	return parts[3]
}

// topicAllowed reports whether arn is one of the comma-separated ARNs the
// provider is configured with. The same list verifies its delivery events on
// /webhooks/, so a provider whose events and inbound mail use different
// topics lists both.
func topicAllowed(configured, arn string) bool {
	if arn == "" {
		return false
	}
	for _, want := range strings.Split(configured, ",") {
		want = strings.TrimSpace(want)
		if want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(arn)) == 1 {
			return true
		}
	}
	return false
}

// verifySNS authenticates an SNS post and returns its envelope.
//
// The topic and the certificate URL are read from the body before it is
// verified. That is safe because both are only used to refuse: the topic is
// covered by the signature, so a genuine message always names the topic it
// was published to, and the certificate URL is pinned before anything is
// fetched from it. The signature itself -- SignatureVersion 1 (SHA-1) and 2
// (SHA-256), the canonical string, and a bounded cache of certificates by URL
// -- is gsmail's.
func (h *WebhookHandler) verifySNS(ctx context.Context, topicARNs string, body []byte) (*gsmail.SNSMessage, error) {
	var envelope struct {
		TopicArn       string `json:"TopicArn"`
		SigningCertURL string `json:"SigningCertURL"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, errUnauthenticated
	}
	if !topicAllowed(topicARNs, envelope.TopicArn) {
		return nil, fmt.Errorf("%w: message is from topic %q", errUnauthenticated, envelope.TopicArn)
	}
	if !snsURLAllowed(envelope.SigningCertURL, topicRegion(envelope.TopicArn)) {
		return nil, fmt.Errorf("%w: signing certificate is not on the topic's SNS endpoint", errUnauthenticated)
	}
	return h.sns.Verify(ctx, body)
}

// receiveSNS authenticates and unwraps a post from an SES provider. It returns
// the message to store, or nil once it has answered the post itself.
func (h *WebhookHandler) receiveSNS(w http.ResponseWriter, r *http.Request, provider *providerEntities.EmailProvider, body []byte) *inboundPayload {
	tenantID, providerID := provider.TenantID, provider.ID

	msg, err := h.verifySNS(r.Context(), provider.WebhookSecret, body)
	if err != nil {
		if snsUnreachable(err) {
			// The certificate could not be fetched from AWS. Nothing says the
			// message is forged, so SNS is asked to retry rather than told it
			// is unauthorised and left to give up on real mail.
			slog.Error("could not verify an SNS post: signing certificate unavailable",
				"tenant_id", tenantID, "provider_id", providerID, "error", err)
			http.Error(w, "Could not verify the message; retry later", http.StatusServiceUnavailable)
			return nil
		}
		slog.Warn("rejecting unauthenticated inbound post",
			"tenant_id", tenantID, "provider_id", providerID, "error", err)
		http.Error(w, "Inbound authentication failed", http.StatusUnauthorized)
		return nil
	}

	switch msg.Type {
	case "SubscriptionConfirmation":
		h.confirmSubscription(w, r, tenantID, providerID, msg)
		return nil

	case "UnsubscribeConfirmation":
		slog.Warn("SES inbound topic unsubscribed; received mail will stop arriving",
			"tenant_id", tenantID, "provider_id", providerID, "topic", msg.TopicArn)
		w.WriteHeader(http.StatusOK)
		return nil
	}

	payload, err := parseSESReceived(msg.Message)
	if err != nil {
		slog.Warn("SNS post is not an SES received-mail notification",
			"tenant_id", tenantID, "provider_id", providerID, "topic", msg.TopicArn, "error", err)
		http.Error(w, "The notification is not received mail", http.StatusBadRequest)
		return nil
	}
	return payload
}

// snsUnreachable reports whether a verification failed because AWS could not
// be asked, rather than because the message is not genuine.
func snsUnreachable(err error) bool {
	return !errors.Is(err, errUnauthenticated) &&
		!errors.Is(err, gsmail.ErrSignatureInvalid) &&
		!errors.Is(err, gsmail.ErrSignatureMissing)
}

// confirmSubscription confirms the topic's subscription to this endpoint. SNS
// delivers nothing until it is confirmed.
//
// By the time this runs the message is signed and its topic is one the
// provider names, so confirming subscribes panmail to a topic the tenant chose
// and nothing else. A provider with no topic configured never gets here: its
// empty secret is refused before any of this, which is what stops a stranger
// subscribing a tenant's endpoint to their own topic. The URL is still
// checked, since following it is an outbound request made on the body's say.
func (h *WebhookHandler) confirmSubscription(w http.ResponseWriter, r *http.Request, tenantID, providerID string, msg *gsmail.SNSMessage) {
	if !snsURLAllowed(msg.SubscribeURL, topicRegion(msg.TopicArn)) {
		slog.Warn("refusing an SNS subscription confirmation to a URL off the topic's SNS endpoint",
			"tenant_id", tenantID, "provider_id", providerID, "url", msg.SubscribeURL)
		http.Error(w, "SubscribeURL is not the topic's SNS endpoint", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, msg.SubscribeURL, nil)
	if err != nil {
		http.Error(w, "Could not confirm the subscription", http.StatusBadGateway)
		return
	}
	resp, err := h.snsHTTP.Do(req)
	if err != nil {
		slog.Error("SNS subscription confirmation failed",
			"error", err, "tenant_id", tenantID, "provider_id", providerID)
		http.Error(w, "Could not confirm the subscription", http.StatusBadGateway)
		return
	}
	gsmail.DrainAndClose(resp.Body)
	if resp.StatusCode != http.StatusOK {
		slog.Error("SNS refused the subscription confirmation",
			"status", resp.StatusCode, "tenant_id", tenantID, "provider_id", providerID)
		http.Error(w, "Could not confirm the subscription", http.StatusBadGateway)
		return
	}

	slog.Info("confirmed the SES inbound SNS subscription",
		"tenant_id", tenantID, "provider_id", providerID, "topic", msg.TopicArn)
	w.WriteHeader(http.StatusOK)
}

// sesReceived is the notification an SES receipt rule publishes for a message
// it received. Only the fields panmail reads.
type sesReceived struct {
	NotificationType string `json:"notificationType"`
	Mail             struct {
		MessageID   string   `json:"messageId"`
		Source      string   `json:"source"`
		Destination []string `json:"destination"`
		Headers     []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
		CommonHeaders struct {
			From    []string `json:"from"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
		} `json:"commonHeaders"`
	} `json:"mail"`
	Receipt struct {
		Action struct {
			Type       string `json:"type"`
			Encoding   string `json:"encoding"`
			BucketName string `json:"bucketName"`
			ObjectKey  string `json:"objectKey"`
		} `json:"action"`
	} `json:"receipt"`

	// Content is the raw message, present only when the rule's action is SNS
	// itself. SES refuses that action for a message over 150 KB.
	Content string `json:"content"`
}

// Headers an S3-delivered message is stored with, naming where SES put it.
const (
	headerSESBucket    = "X-Panmail-Ses-S3-Bucket"
	headerSESObjectKey = "X-Panmail-Ses-S3-Object-Key"
)

// parseSESReceived reads the message out of an SES "Received" notification.
//
// With an SNS action the notification carries the whole message, and it is
// parsed like any other. With any other action -- S3 being the one used for
// mail over 150 KB -- it carries only the headers SES read, and panmail does
// not fetch from the bucket: it stores what arrived, with the bucket and key
// among the headers so whoever consumes the message can.
func parseSESReceived(raw string) (*inboundPayload, error) {
	var n sesReceived
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		return nil, fmt.Errorf("unparseable notification: %w", err)
	}
	if n.NotificationType != "Received" {
		return nil, fmt.Errorf("notification type %q", n.NotificationType)
	}

	p := &inboundPayload{
		// SES's id for the received message, the same on every retry and on
		// every topic it is published to.
		EventID: n.Mail.MessageID,
		Subject: n.Mail.CommonHeaders.Subject,
		To:      n.Mail.CommonHeaders.To,
	}
	if len(n.Mail.CommonHeaders.From) > 0 {
		p.From = n.Mail.CommonHeaders.From[0]
	}
	if p.From == "" {
		p.From = n.Mail.Source
	}
	if len(p.To) == 0 {
		p.To = n.Mail.Destination
	}
	if len(n.Mail.Headers) > 0 {
		p.Headers = make(map[string]string, len(n.Mail.Headers))
		for _, h := range n.Mail.Headers {
			if _, seen := p.Headers[h.Name]; !seen {
				p.Headers[h.Name] = h.Value
			}
		}
	}

	if n.Content == "" {
		if n.Receipt.Action.BucketName != "" {
			if p.Headers == nil {
				p.Headers = map[string]string{}
			}
			p.Headers[headerSESBucket] = n.Receipt.Action.BucketName
			p.Headers[headerSESObjectKey] = n.Receipt.Action.ObjectKey
		}
		if p.empty() {
			return nil, errors.New("notification carries no message")
		}
		return p, nil
	}

	content := []byte(n.Content)
	if strings.EqualFold(n.Receipt.Action.Encoding, "BASE64") {
		decoded, err := base64.StdEncoding.DecodeString(n.Content)
		if err != nil {
			return nil, fmt.Errorf("content is not base64: %w", err)
		}
		content = decoded
	}

	// The whole message, so a delivery report relayed through SES shows its
	// parts to the bounce check like one posted raw by any other service.
	p.Raw = string(content)

	parsed, err := smtpmime.NewParser(0, 0).Parse(bytes.NewReader(content), n.Mail.Source)
	if err != nil {
		// SES accepted this message, so it is mail whatever its MIME says.
		// Refusing it would have SNS retry it until it gave up; keeping the
		// raw text and the headers SES read loses nothing.
		p.Text = string(content)
		return p, nil
	}
	req := parsed.Request
	p.From = req.From
	if len(req.To) > 0 {
		p.To = req.To
	}
	if req.Subject != "" {
		p.Subject = req.Subject
	}
	p.Text = req.BodyText
	p.HTML = req.BodyHtml
	p.readRaw()
	return p, nil
}
