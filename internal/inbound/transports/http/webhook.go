package http

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"github.com/gsoultan/panmail/internal/inbound/usecases"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// maxInboundBody caps an inbound message payload. Generous enough for a mail
// body with inline content, small enough that a flood cannot exhaust memory.
const maxInboundBody = 25 << 20 // 25 MiB

// maxFormMemory is how much of a multipart payload is held in memory before
// file parts spill to disk. The body is already capped, so this only decides
// where attachments the handler ignores end up while the form is parsed.
const maxFormMemory = 8 << 20

// SignatureHeader carries the HMAC an inbound post may be signed with. The
// same header, and the same scheme, as the generic format on /webhooks/.
const SignatureHeader = "X-Panmail-Signature"

var (
	errUnauthenticated = errors.New("inbound post is not authenticated")
)

// WebhookHandler receives mail a provider has parsed and posts on to
// /inbound/{tenant_id}/{provider_id}.
//
// It is authenticated with the provider's webhook secret, the same per-provider
// credential /webhooks/ verifies delivery events with. It used to take the
// tenant from a tenant_id field in the body and check nothing, so anyone on the
// internet could put mail in any tenant's inbox, fire its MAIL_INBOUND webhooks,
// and -- with a bounce-shaped subject -- suppress addresses. The tenant now comes
// from the provider the credential belongs to, and the body is not asked.
type WebhookHandler struct {
	usecase      usecases.InboundUsecase
	providerRepo providerStores.Repository
}

func NewWebhookHandler(usecase usecases.InboundUsecase, providerRepo providerStores.Repository) *WebhookHandler {
	return &WebhookHandler{usecase: usecase, providerRepo: providerRepo}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Expected path: /inbound/{tenant_id}/{provider_id}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		http.Error(w, "Invalid inbound URL. Expected /inbound/{tenant_id}/{provider_id}", http.StatusNotFound)
		return
	}
	tenantID, providerID := parts[1], parts[2]

	// The body is capped: reading an unbounded request into memory on a public
	// endpoint is a denial of service anyone can trigger. It is read whole
	// because the signature covers the raw bytes, and the parse below then
	// works from the same buffer.
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInboundBody))
	if err != nil {
		http.Error(w, "Request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}

	// Authenticated before anything is parsed, so an anonymous caller learns
	// nothing about how the payload would have been read.
	if err := h.authenticate(r, tenantID, providerID, body); err != nil {
		slog.Warn("rejecting unauthenticated inbound post",
			"tenant_id", tenantID, "provider_id", providerID, "error", err)
		http.Error(w, "Inbound authentication failed", http.StatusUnauthorized)
		return
	}

	payload, status, refusal := parsePayload(r.Header.Get("Content-Type"), body)
	if payload == nil {
		http.Error(w, refusal, status)
		return
	}

	// Only the raw message, when the service posts it, can show a delivery
	// report's structure: the parsed fields carry text, not parts. A post
	// without it is stored as mail and never records a bounce.
	var report *usecases.DeliveryReport
	if payload.raw != nil {
		report = usecases.ParseDeliveryReport(payload.raw.Attachments)
	}

	email := &panmailv1.InboundEmail{
		Id:        uuid.New().String(),
		TenantId:  tenantID,
		From:      payload.From,
		To:        payload.To,
		Subject:   payload.Subject,
		BodyHtml:  payload.HTML,
		BodyText:  payload.Text,
		Timestamp: timestamppb.New(time.Now()),
		Headers:   payload.Headers,
	}

	if err := h.usecase.Process(r.Context(), email, report); err != nil {
		// Logged, not returned. Process writes to the inbound store, the outbox
		// and any webhook subscriptions, so its error can carry filesystem
		// paths, the database user and host, or an internal endpoint's address.
		// The provider on the other end needs a status code to decide whether
		// to retry; it has no use for any of that.
		slog.Error("failed to process an inbound email",
			"error", err, "tenant_id", email.TenantId, "id", email.Id)
		http.Error(w, "Could not process the message", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// authenticate checks the post against the provider's webhook secret, which it
// may present either way an inbound-parse service can:
//
//   - X-Panmail-Signature: sha256=<hex HMAC-SHA256 of the raw body>, for a
//     relay that can sign; or
//   - HTTP Basic credentials, for a service that can only be given a URL --
//     https://user:pass@host/inbound/... with a secret of "user:pass", or
//     https://:secret@host/... with a secret that has no colon. The same
//     convention as Postmark's delivery webhooks.
//
// Unknown tenant, unknown provider and wrong secret are all the same refusal,
// so the endpoint cannot be used to enumerate ids.
func (h *WebhookHandler) authenticate(r *http.Request, tenantID, providerID string, body []byte) error {
	secret, err := h.inboundSecret(r.Context(), tenantID, providerID)
	if err != nil {
		return err
	}

	if signature := r.Header.Get(SignatureHeader); signature != "" {
		return verifySignature(secret, signature, body)
	}
	if r.Header.Get("Authorization") != "" {
		user, pass, found := strings.Cut(secret, ":")
		if !found {
			user, pass = "", secret
		}
		if err := (gsmail.PostmarkVerifier{Username: user, Password: pass}).Verify(r.Header); err != nil {
			return errUnauthenticated
		}
		return nil
	}
	return errUnauthenticated
}

func (h *WebhookHandler) inboundSecret(ctx context.Context, tenantID, providerID string) (string, error) {
	if h.providerRepo == nil {
		return "", errUnauthenticated
	}
	provider, err := h.providerRepo.GetByID(ctx, tenantID, providerID)
	if err != nil || provider == nil || provider.WebhookSecret == "" {
		return "", errUnauthenticated
	}

	// For these two the stored value is verification material, not a shared
	// secret: SendGrid's is an ECDSA *public* key and SES's is a topic ARN.
	// Accepting either as a password would let anyone who has seen it post
	// mail, so a tenant receiving through one of them points inbound at a
	// provider whose secret is a secret.
	switch provider.Type {
	case panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID, panmailv1.ProviderType_PROVIDER_TYPE_SES:
		return "", errUnauthenticated
	}
	return provider.WebhookSecret, nil
}

func verifySignature(secret, signature string, body []byte) error {
	provided, _ := strings.CutPrefix(signature, "sha256=")
	want, err := hex.DecodeString(provided)
	if err != nil {
		return errUnauthenticated
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), want) {
		return errUnauthenticated
	}
	return nil
}

type inboundPayload struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	HTML    string            `json:"html"`
	Text    string            `json:"text"`
	Headers map[string]string `json:"headers"`

	// Raw is the whole message as received, which some services post
	// alongside or instead of the parsed fields (SendGrid's "email" field
	// with "send raw" on). It is the only source of a message's parts.
	Raw string `json:"raw"`

	// raw is Raw parsed, nil when there was none or it did not parse.
	raw *gsmail.Email
}

// readRaw parses Raw and fills in any field the service did not post.
func (p *inboundPayload) readRaw() {
	if p.Raw == "" {
		return
	}
	parsed, err := gsmail.ParseRawEmail([]byte(p.Raw))
	if err != nil {
		return
	}
	p.raw = &parsed
	if p.From == "" {
		p.From = parsed.From
	}
	if len(p.To) == 0 {
		p.To = parsed.To
	}
	if p.Subject == "" {
		p.Subject = parsed.Subject
	}
	if p.Text == "" {
		p.Text = string(parsed.Body)
	}
	if p.HTML == "" {
		p.HTML = string(parsed.HTMLBody)
	}
	if len(p.Headers) == 0 {
		p.Headers = parsed.Headers
	}
}

func (p *inboundPayload) empty() bool {
	return p.From == "" && len(p.To) == 0 && p.Subject == "" && p.HTML == "" && p.Text == ""
}

// parsePayload reads the message from the body according to its content type.
// When it cannot, it returns a nil payload with the status to answer with and a
// fixed description of the refusal -- never a parser's error text, which is
// not written for the caller.
//
// The form branch used to call r.ParseMultipartForm after the body had already
// been read into memory, so it always failed, silently, and an empty message
// was stored and acknowledged. Every format now parses from the buffer, and a
// post that yields nothing is refused rather than accepted as a blank email.
func parsePayload(contentType string, body []byte) (*inboundPayload, int, string) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, http.StatusUnsupportedMediaType, "missing or malformed Content-Type"
	}

	var p inboundPayload
	switch mediaType {
	case "application/json":
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, http.StatusBadRequest, "invalid JSON payload"
		}

	case "multipart/form-data":
		boundary := params["boundary"]
		if boundary == "" {
			return nil, http.StatusBadRequest, "multipart payload has no boundary"
		}
		form, err := multipart.NewReader(bytes.NewReader(body), boundary).ReadForm(maxFormMemory)
		if err != nil {
			return nil, http.StatusBadRequest, "invalid multipart payload"
		}
		defer form.RemoveAll()
		p.fromForm(url.Values(form.Value))

	case "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, http.StatusBadRequest, "invalid form payload"
		}
		p.fromForm(values)

	default:
		return nil, http.StatusUnsupportedMediaType, "unsupported Content-Type"
	}

	p.readRaw()
	if p.empty() {
		return nil, http.StatusBadRequest, "the post carried no message"
	}
	return &p, 0, ""
}

// fromForm reads the field names inbound-parse services post: from, to,
// subject, text, html, and headers as a raw header block.
func (p *inboundPayload) fromForm(values url.Values) {
	p.From = values.Get("from")
	p.Subject = values.Get("subject")
	p.Text = values.Get("text")
	p.HTML = values.Get("html")
	p.Raw = values.Get("email")

	for _, field := range values["to"] {
		for _, addr := range strings.Split(field, ",") {
			if addr = strings.TrimSpace(addr); addr != "" {
				p.To = append(p.To, addr)
			}
		}
	}

	if raw := values.Get("headers"); raw != "" {
		// A block that stops parsing part-way still yields the headers read
		// before the fault, which is more use than none.
		header, _ := textproto.NewReader(bufio.NewReader(strings.NewReader(raw + "\r\n\r\n"))).ReadMIMEHeader()
		if len(header) > 0 {
			p.Headers = make(map[string]string, len(header))
			for name, vals := range header {
				p.Headers[name] = vals[0]
			}
		}
	}
}
