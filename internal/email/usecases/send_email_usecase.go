package usecases

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/gsmail/outlook"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/email/repositories/stores"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"github.com/gsoultan/panmail/internal/emailfilter"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
	"github.com/gsoultan/panmail/internal/ratelimit"
	suppressionStores "github.com/gsoultan/panmail/internal/suppression/repositories/stores"
	templateEntities "github.com/gsoultan/panmail/internal/template/repositories/entities"
	templateStores "github.com/gsoultan/panmail/internal/template/repositories/stores"
	"github.com/gsoultan/panmail/pkg/cache"
	"github.com/gsoultan/panmail/pkg/tracking"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	trackingKindOpen  = "open"
	trackingKindClick = "click"
	// Must match the value the unsubscribe handler verifies against. The kind
	// is part of the signed payload, so a mismatch rejects every link.
	trackingKindUnsubscribe = "unsubscribe"

	providerCacheTTL      = time.Minute
	templateCacheTTL      = time.Minute
	maxProvidersPerTenant = 1000
)

// SendEmailDeps groups the collaborators a send needs. Passing them as one
// value keeps the constructor readable as the pipeline grows.
type SendEmailDeps struct {
	ProviderRepo    providerStores.Repository
	TemplateRepo    templateStores.TemplateRepository
	SuppressionRepo suppressionStores.SuppressionRepository
	OutboxRepo      stores.OutboxRepository
	EventUsecase    eventusecases.ProcessEventUsecase
	ProviderFactory providerEntities.ProviderFactory
	Renderer        TemplateRenderer
	// BaseURL is the static public URL. Tests and any deployment where it
	// never changes set this and nothing else.
	BaseURL string

	// BaseURLSource, when set, wins over BaseURL and is read on every use, so
	// a base URL saved on the settings page reaches this instance without a
	// restart. It reads a published pointer rather than the database — this is
	// the send path, and a query behind every tracking link is the regression
	// the send path was measured to remove. See system_settings.Provider.
	BaseURLSource  BaseURLSource
	TrackingSigner *tracking.Signer

	// Both optional. Without them the send path is unlimited, which is what
	// every deployment had before the ceiling existed.
	Limiter    *ratelimit.Limiter
	SendLimits SendLimits

	// Screener is optional too. A nil one sends every message, which is what
	// a deployment with no filter rules configured must do.
	Screener emailfilter.Screener
}

type sendEmailUsecase struct {
	limiter         *ratelimit.Limiter
	sendLimits      SendLimits
	backlogCounts   *cache.TTLCache[int64]
	providerRepo    providerStores.Repository
	templateRepo    templateStores.TemplateRepository
	suppressionRepo suppressionStores.SuppressionRepository
	outboxRepo      stores.OutboxRepository
	eventUsecase    eventusecases.ProcessEventUsecase
	providerFactory providerEntities.ProviderFactory
	renderer        TemplateRenderer
	staticBaseURL   string
	baseURLSource   BaseURLSource
	trackingSigner  *tracking.Signer
	queueWorker     QueueWorker
	screener        emailfilter.Screener

	providerCache *cache.TTLCache[[]*providerEntities.EmailProvider]
	templateCache *cache.TTLCache[*templateEntities.Template]
}

// BaseURLSource supplies the public URL links are built from.
type BaseURLSource interface {
	BaseURL() string
}

// baseURL is the public URL in force, without a trailing slash. Empty disables
// tracking and unsubscribe links rather than emitting relative ones.
func (u *sendEmailUsecase) baseURL() string {
	if u.baseURLSource != nil {
		if v := u.baseURLSource.BaseURL(); v != "" {
			return v
		}
	}
	return u.staticBaseURL
}

func NewSendEmailUsecase(deps SendEmailDeps) SendEmailUsecase {
	return &sendEmailUsecase{
		limiter:         deps.Limiter,
		sendLimits:      deps.SendLimits,
		backlogCounts:   cache.New[int64](backlogCountTTL),
		providerRepo:    deps.ProviderRepo,
		templateRepo:    deps.TemplateRepo,
		suppressionRepo: deps.SuppressionRepo,
		outboxRepo:      deps.OutboxRepo,
		eventUsecase:    deps.EventUsecase,
		providerFactory: deps.ProviderFactory,
		renderer:        deps.Renderer,
		staticBaseURL:   strings.TrimSuffix(deps.BaseURL, "/"),
		baseURLSource:   deps.BaseURLSource,
		trackingSigner:  deps.TrackingSigner,
		screener:        deps.Screener,
		providerCache:   cache.New[[]*providerEntities.EmailProvider](providerCacheTTL),
		templateCache:   cache.New[*templateEntities.Template](templateCacheTTL),
	}
}

type contextKey string

const (
	SkipOutboxKey contextKey = "skip_outbox"
	MessageIDKey  contextKey = "message_id"
)

func (u *sendEmailUsecase) RegisterQueueWorker(w QueueWorker) {
	u.queueWorker = w
}

func (u *sendEmailUsecase) SendEmail(ctx context.Context, tenantID string, req *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id is mandatory")
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, fmt.Errorf("invalid tenant id format: %s. tenant id must be a valid UUID", tenantID)
	}
	if tenantID == uuid.Nil.String() {
		return nil, fmt.Errorf("tenant id cannot be nil uuid (00000000-0000-0000-0000-000000000000)")
	}

	if req.From == "" {
		return nil, fmt.Errorf("from address is mandatory")
	}
	if req.ProviderId == "" {
		return nil, fmt.Errorf("provider id is mandatory")
	}

	// Validate ProviderId is a valid non-nil UUID
	if _, err := uuid.Parse(req.ProviderId); err != nil {
		return nil, fmt.Errorf("invalid provider id format: %s. provider id must be a valid UUID", req.ProviderId)
	}
	if req.ProviderId == uuid.Nil.String() {
		return nil, fmt.Errorf("provider id cannot be nil uuid (00000000-0000-0000-0000-000000000000)")
	}

	if len(req.To) == 0 {
		return nil, fmt.Errorf("at least one recipient is required")
	}

	// Reject malformed addresses here rather than letting the SMTP server do
	// it. A rejected RCPT TO counts against the sending reputation, and enough
	// of them get a sending domain throttled or blocked — so an address that
	// cannot possibly be deliverable should never reach a provider at all.
	//
	// The From address is checked too: a malformed one fails every recipient of
	// the message, which is worth catching before anything is queued.
	// ParseEmailAddress rather than ValidateEmailSyntax, because the latter
	// wants a bare addr-spec and callers legitimately send the display-name
	// form, "Alice Smith <alice@example.com>". Rejecting that would make
	// validation the thing blocking real mail.
	if _, err := gsmail.ParseEmailAddress(req.From); err != nil {
		return nil, fmt.Errorf("invalid from address %q: %w", req.From, err)
	}
	// Validated, de-duplicated and normalised in one pass, because parsing an
	// address allocates and this used to do it twice for every recipient.
	recipients, err := resolveRecipients(req.To, req.Cc, req.Bcc)
	if err != nil {
		return nil, err
	}

	// Same reasoning as the provider check below, for the same reason: a
	// message that cannot be delivered must be refused here, while the caller
	// is still listening, rather than queued for a worker to fail on.
	if err := validateAttachments(req); err != nil {
		return nil, err
	}

	// Fail fast on a provider that does not exist, rather than queueing a
	// message that can never be delivered.
	provider, err := u.getProvider(ctx, tenantID, req.ProviderId)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}
	if provider == nil {
		return nil, fmt.Errorf("provider not found: %s", req.ProviderId)
	}

	// Anti-spoofing is enforced by the provider's AllowedDomains list in
	// doSend, which checks the From address against the domains the operator
	// authorised for that provider.
	//
	// There used to be a second check here comparing the From domain with the
	// SMTP *host* domain. It rejected every hosted ESP — sending
	// noreply@yourcompany.com through smtp.sendgrid.net is the normal case, not
	// an attack — while adding nothing against a real spoofer, who controls the
	// From address and the provider alike. Do not reintroduce it: relaying
	// authority is a property of the account, not of the hostname's domain.

	// 1. Worker mode (Actual Sending)
	if skip, ok := ctx.Value(SkipOutboxKey).(bool); ok && skip {
		messageID, _ := ctx.Value(MessageIDKey).(string)
		return u.doSend(ctx, tenantID, req, messageID)
	}

	// 2. Client mode (Async Queuing)

	// The send rate is charged here and only here.
	//
	// SendEmail is called twice for every message — once by a client, which
	// queues it, and again by the outbox worker, which delivers it. Charging
	// above this branch therefore billed each message twice and halved the
	// rate an operator had configured. Admission is the half worth keeping: it
	// is the only point at which anyone is waiting to be told to slow down,
	// and a message already accepted should not be refused later for a
	// condition it did not cause.
	//
	// Everything admitted is eventually delivered, so egress over time follows
	// the admission rate. The gap is a backlog drained after an outage, which
	// leaves faster than it arrived; pacing that needs a limit on queue depth,
	// which is a different measurement from this one.
	if err := u.checkSendRate(ctx, tenantID, recipientCount(req.To, req.Cc, req.Bcc)); err != nil {
		return nil, err
	}

	messageID := uuid.New().String()

	// Extract domain from From address
	fromParts := strings.Split(req.From, "@")
	if len(fromParts) != 2 {
		return nil, fmt.Errorf("invalid from address: %s", req.From)
	}

	// Check suppressions for every recipient, in one round trip.
	//
	// This was a query per recipient, which made a hundred-recipient send cost
	// a hundred sequential trips to a database usually on another host — the
	// dominant cost of admitting a large message. The check is unchanged:
	// every address is still looked up, still scoped to the tenant, and the
	// first suppressed recipient still refuses the whole message.
	suppressed, err := u.suppressionsFor(ctx, tenantID, recipients.normalised)
	if err != nil {
		slog.Error("failed to check suppression", "error", err, "id", messageID)
		return nil, err
	}

	for i, recipient := range recipients.addresses {
		if reason, ok := suppressed[recipients.normalised[i]]; ok {
			_ = u.RecordEvent(ctx, tenantID, "", messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DROPPED, recipient, "", reason, nil)
			return nil, fmt.Errorf("recipient %s is suppressed: %s", recipient, reason)
		}
		// Record initial PENDING event (queued in outbox)
		if err := u.RecordEvent(ctx, tenantID, req.ProviderId, messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING, recipient, "", "", nil); err != nil {
			slog.Error("failed to record pending event", "error", err, "id", messageID)
		}
	}

	subject, bodyHTML, bodyText, err := u.renderTemplate(ctx, tenantID, req)
	if err != nil {
		slog.Error("failed to render template", "error", err, "id", messageID)
		return nil, err
	}

	// Filter rules run here: in client mode only, and after the template has
	// been rendered.
	//
	// Client mode only, because the outbox worker calls SendEmail a second
	// time to deliver. A message screened again on that pass would be held
	// again the instant a reviewer released it — the release would hand it
	// straight back to the rule that stopped it, and nothing would ever leave.
	// The worker-mode branch returns long before this line, which is what
	// makes that true.
	//
	// After rendering, because a rule about the subject or the body has to see
	// what the recipient will see. Screening the raw request instead would
	// silently miss every templated message, and templated messages are most
	// of what a gateway sends.
	outboxStatus := entities.OutboxStatusPending
	var held *emailfilter.FilteredMessage

	if u.screener != nil {
		screened := emailfilter.Message{
			From: req.From, To: req.To, Cc: req.Cc, Bcc: req.Bcc,
			Subject: subject, HTML: bodyHTML, Text: bodyText,
			Attachments: filterAttachments(req.Attachments),
			Size:        approximateSize(subject, bodyHTML, bodyText, req.Attachments),
			ProviderID:  req.ProviderId,
		}

		decision, err := u.screener.Screen(ctx, tenantID, emailfilter.DirectionOutbound, screened)
		if err != nil {
			// Reported, not swallowed. Filtering exists to stop things, and a
			// gateway that sends everything whenever its database is briefly
			// unreachable is a filter that fails open at the one moment it
			// was supposed to work. The caller can retry.
			slog.Error("failed to screen outbound message", "error", err, "id", messageID)
			return nil, fmt.Errorf("failed to screen message: %w", err)
		}

		if decision.Action != "" {
			record := emailfilter.NewFilteredMessage(tenantID, emailfilter.DirectionOutbound, screened, decision)
			record.MessageID = messageID

			switch decision.Action {
			case emailfilter.ActionReject:
				// Refused while the caller is still listening. A rejection
				// nobody is told about is a message that silently vanished.
				if err := u.screener.Record(ctx, &record); err != nil {
					slog.Error("failed to record a rejected message", "error", err, "id", messageID)
				}
				for _, recipient := range recipients.addresses {
					_ = u.RecordEvent(ctx, tenantID, req.ProviderId, messageID,
						panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED, recipient, "",
						"filter rule: "+record.RuleName, nil)
				}
				return nil, fmt.Errorf("message refused by filter rule %q", record.RuleName)

			case emailfilter.ActionHold:
				// The outbox is where it waits. The claim query takes PENDING,
				// DEFERRED and expired SENDING rows, so a HELD one is already
				// invisible to the worker — the bytes are durable, bounded by
				// the outbox's own retention, and releasing is a status flip
				// rather than a second copy of the same request.
				outboxStatus = entities.OutboxStatusHeld
				record.PayloadRef = messageID
				held = &record

			case emailfilter.ActionTag:
				// Delivered, and recorded so the rule can be watched before
				// anyone trusts it enough to hold on.
				if err := u.screener.Record(ctx, &record); err != nil {
					slog.Error("failed to record a tagged message", "error", err, "id", messageID)
				}
			}
		}
	}

	// Save message content for analytics (before tracking injection for clean preview)
	if err := u.eventUsecase.SaveMessage(ctx, &panmailv1.EmailMessage{
		Id:          messageID,
		TenantId:    tenantID,
		ProviderId:  req.ProviderId,
		From:        req.From,
		To:          req.To,
		Cc:          req.Cc,
		Bcc:         req.Bcc,
		Subject:     subject,
		BodyHtml:    bodyHTML,
		BodyText:    bodyText,
		Attachments: req.Attachments,
	}); err != nil {
		slog.Error("failed to save message for analytics", "error", err, "id", messageID)
	}

	// Save to outbox for the worker to pick up
	reqBytes, err := protojson.Marshal(req)
	if err != nil {
		slog.Error("failed to marshal outbox email request", "error", err, "id", messageID)
		return nil, fmt.Errorf("failed to enqueue email: %w", err)
	}
	outboxEmail := &entities.OutboxEmail{
		ID:          messageID,
		TenantID:    tenantID,
		Request:     reqBytes,
		Status:      outboxStatus,
		RetryCount:  0,
		NextRetryAt: time.Now(), // Try immediately
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := u.outboxRepo.Create(ctx, outboxEmail); err != nil {
		slog.Error("failed to create outbox email", "error", err, "id", messageID)
		return nil, fmt.Errorf("failed to enqueue email: %w", err)
	}

	// Recorded only after the payload is durably in the outbox. The other
	// order would leave a review queue entry pointing at a message that does
	// not exist if the process died between the two writes.
	if held != nil {
		if err := u.screener.Record(ctx, held); err != nil {
			slog.Error("failed to record a held message", "error", err, "id", messageID)
		}
		slog.Info("email held for review", "id", messageID, "tenant_id", tenantID, "rule", held.RuleName)
		return &panmailv1.SendEmailResponse{
			MessageId: messageID,
			Status:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING,
		}, nil
	}

	// Trigger worker to process immediately
	if u.queueWorker != nil {
		u.queueWorker.Trigger()
	}

	slog.Info("email enqueued successfully", "id", messageID, "tenant_id", tenantID)

	return &panmailv1.SendEmailResponse{
		MessageId: messageID,
		Status:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING,
	}, nil
}

func (u *sendEmailUsecase) doSend(ctx context.Context, tenantID string, req *panmailv1.SendEmailRequest, messageID string) (*panmailv1.SendEmailResponse, error) {
	if messageID == "" {
		messageID = uuid.New().String()
	}

	// Extract domain from From address
	fromParts := strings.Split(req.From, "@")
	if len(fromParts) != 2 {
		return nil, fmt.Errorf("invalid from address: %s", req.From)
	}
	fromDomain := strings.ToLower(fromParts[1])

	// The address headers every copy carries, as opposed to the single address
	// each copy is delivered to. Bcc is absent by design: it is the one list
	// that must not appear in a header, and its members still receive their own
	// copy because the send loop iterates over To, Cc and Bcc alike.
	visibleTo := append([]string(nil), req.To...)
	visibleCc := append([]string(nil), req.Cc...)

	var providers []*providerEntities.EmailProvider
	if req.ProviderId != "" {
		// Try to find in cached list first
		all, _ := u.getProviders(ctx, tenantID)
		var p *providerEntities.EmailProvider
		for _, item := range all {
			if item.ID == req.ProviderId {
				p = item
				break
			}
		}

		if p == nil {
			// Fallback to DB
			p, _ = u.providerRepo.GetByID(ctx, tenantID, req.ProviderId)
		}

		if p != nil {
			// Check if this provider is allowed to send for this domain
			if len(p.AllowedDomains) > 0 {
				allowed := false
				for _, d := range p.AllowedDomains {
					if strings.ToLower(d) == fromDomain {
						allowed = true
						break
					}
				}
				if !allowed {
					return nil, fmt.Errorf("provider %s is not authorized to send for domain %s", p.Name, fromDomain)
				}
			}
			providers = append(providers, p)
		}
	}

	if len(providers) == 0 {
		allProviders, err := u.getProviders(ctx, tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to list providers: %w", err)
		}

		var domainSpecific []*providerEntities.EmailProvider
		var generic []*providerEntities.EmailProvider

		for _, p := range allProviders {
			// Skip receivers
			if p.Type == panmailv1.ProviderType_PROVIDER_TYPE_IMAP || p.Type == panmailv1.ProviderType_PROVIDER_TYPE_POP3 {
				continue
			}

			if len(p.AllowedDomains) > 0 {
				for _, d := range p.AllowedDomains {
					if strings.ToLower(d) == fromDomain {
						domainSpecific = append(domainSpecific, p)
						break
					}
				}
			} else {
				generic = append(generic, p)
			}
		}

		if len(domainSpecific) > 0 {
			providers = domainSpecific
		} else {
			providers = generic
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no email providers available for tenant %s", tenantID)
	}

	subject, bodyHTML, bodyText, err := u.renderTemplate(ctx, tenantID, req)
	if err != nil {
		slog.Error("failed to render template for sending", "error", err, "id", messageID)
		return nil, err
	}

	// Fetch existing events to ensure idempotency (prevent duplicate sends on retry)
	existingEvents, err := u.eventUsecase.ListByMessageID(ctx, tenantID, messageID)
	if err != nil {
		slog.Error("failed to list existing events for message", "error", err, "id", messageID)
	}
	deliveredMap := make(map[string]bool)
	for _, ee := range existingEvents {
		if ee.Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED {
			deliveredMap[ee.Recipient] = true
		}
	}

	recipients := uniqueRecipients(req.To, req.Cc, req.Bcc)

	slog.Info("starting actual delivery", "id", messageID, "recipient_count", len(recipients), "provider_count", len(providers))

	// We iterate through recipients to support individual tracking and status
	var deliveryErrors []error
	for _, recipient := range recipients {
		if deliveredMap[recipient] {
			slog.Info("email already delivered to recipient, skipping", "id", messageID, "recipient", recipient)
			continue
		}

		slog.Info("processing recipient", "id", messageID, "recipient", recipient)

		sent := false
		var recipientErr error
		for _, p := range providers {
			// Skip receivers
			if p.Type == panmailv1.ProviderType_PROVIDER_TYPE_IMAP || p.Type == panmailv1.ProviderType_PROVIDER_TYPE_POP3 {
				continue
			}

			slog.Info("trying provider", "id", messageID, "provider", p.Name, "type", p.Type.String())

			sender, err := u.providerFactory.CreateSender(p)
			if err != nil {
				slog.Error("failed to create sender for provider", "error", err, "provider", p.Name)
				recipientErr = err
				continue
			}

			// Inject tracking per recipient
			currentBodyHTML := bodyHTML
			if currentBodyHTML != "" && u.baseURL() != "" {
				currentBodyHTML = u.injectTracking(tenantID, messageID, recipient, currentBodyHTML)
			}
			currentBodyHTML = hardenForOutlook(currentBodyHTML)

			// One copy per recipient, because the tracking pixel and the signed
			// links above are personal to this address. The headers still name
			// the whole visible audience, so a Cc recipient can see who else
			// was copied, while Envelope keeps this copy going to one address.
			//
			// Without Envelope these two requirements are mutually exclusive:
			// populating Cc would add every Cc address to RCPT TO on every
			// iteration, so each of them would receive one copy per recipient.
			// Leaving Cc empty was the previous behaviour, and it silently
			// turned every Cc into a Bcc.
			//
			// Bcc is never set as a header — a blind address must not be
			// disclosed — and reaches its own copy through Envelope when the
			// loop gets to it.
			msg := gsmail.Email{
				From:     req.From,
				To:       visibleTo,
				Cc:       visibleCc,
				Envelope: []string{recipient},
				Subject:  subject,
				Body:     []byte(bodyText),
				HTMLBody: []byte(currentBodyHTML),
			}

			// Gmail and Yahoo have required the RFC 8058 header pair from bulk
			// senders since February 2024, and they measure compliance across a
			// sending domain — one campaign without it degrades delivery for
			// every other message from that domain.
			//
			// The link is per recipient and signed, so the endpoint cannot be
			// used to suppress an address the caller was never sending to.
			// A failure is logged rather than fatal: refusing to send would turn
			// a missing base URL into a total outage, and the message is still
			// deliverable without the header.
			if err := u.setUnsubscribeHeaders(&msg, tenantID, messageID, recipient); err != nil {
				slog.Warn("sending without one-click unsubscribe headers",
					"error", err, "id", messageID, "recipient", recipient)
			}

			for _, a := range req.Attachments {
				msg.Attachments = append(msg.Attachments, gsmail.Attachment{
					Filename:    a.Filename,
					ContentType: a.ContentType,
					Data:        a.Content,
				})
			}

			// SENT is recorded only once the provider has actually accepted the
			// message. Recording it before the attempt counted failures as
			// sends and inflated both the Sent metric and the msgs/sec gauge.
			err = sender.Send(ctx, msg)
			if err == nil {
				slog.Info("email delivered successfully", "id", messageID, "provider", p.Name, "recipient", recipient)
				_ = u.RecordEvent(ctx, tenantID, p.ID, messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, recipient, subject, "", nil)
				_ = u.RecordEvent(ctx, tenantID, p.ID, messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, recipient, subject, "", nil)
				sent = true
				break
			}
			recipientErr = err
			slog.Error("provider delivery failed", "provider", p.Name, "error", err, "id", messageID, "recipient", recipient)
			// Record DEFERRED event for this attempt
			_ = u.RecordEvent(ctx, tenantID, p.ID, messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DEFERRED, recipient, subject, err.Error(), nil)
		}

		if !sent && recipientErr != nil {
			// Failed all providers for this recipient
			slog.Error("failed to deliver email to recipient via all providers", "id", messageID, "recipient", recipient, "error", recipientErr)
			deliveryErrors = append(deliveryErrors, fmt.Errorf("failed to deliver to %s: %w", recipient, recipientErr))
		}
	}

	if len(deliveryErrors) > 0 && len(deliveryErrors) == len(recipients) {
		// All recipients failed
		return nil, errors.Join(deliveryErrors...)
	}

	return &panmailv1.SendEmailResponse{
		MessageId: messageID,
		Status:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
	}, nil
}

var (
	hrefRegexp = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)
)

// injectTracking adds the open pixel and rewrites links to route through the
// gateway. Every generated URL is signed, so the resulting events can be
// trusted and the click endpoint cannot be turned into an open redirect.
// hardenForOutlook adds the markup Word's rendering engine needs to HTML that
// does not already have it.
//
// The visual builder emits a document that is already hardened, and marks it, so
// this leaves that untouched — running the conversion over it would append a
// second OfficeDocumentSettings block and two more stylesheets, roughly 3KB of
// duplication with competing rules, for one rule it did not already have.
//
// The point is everything else. HTML uploaded as a file, or supplied straight to
// the API as body_html, previously reached the recipient with no Outlook
// handling at all — no VML namespaces, no DPI settings, no table spacing fixes.
// The builder was the only path that got any of it, and it is not the only path
// that sends mail.
func hardenForOutlook(html string) string {
	if html == "" || outlook.AlreadyConverted([]byte(html)) {
		return html
	}
	return string(outlook.ToOutlookHTML([]byte(html)))
}

func (u *sendEmailUsecase) injectTracking(tenantID, messageID, recipient, htmlContent string) string {
	if u.baseURL() == "" || u.trackingSigner == nil {
		return htmlContent
	}

	recipientEncoded := base64.RawURLEncoding.EncodeToString([]byte(recipient))

	htmlContent = u.injectPixel(htmlContent, tenantID, messageID, recipient, recipientEncoded)
	return u.rewriteLinks(htmlContent, tenantID, messageID, recipient, recipientEncoded)
}

// setUnsubscribeHeaders adds the RFC 8058 pair, List-Unsubscribe and
// List-Unsubscribe-Post, pointing at this gateway's signed endpoint.
//
// gsmail requires at least one https target, because one-click unsubscribe
// works by the mailbox provider POSTing to it — a mailto: target alone cannot
// satisfy the requirement and would make the pair invalid.
func (u *sendEmailUsecase) setUnsubscribeHeaders(msg *gsmail.Email, tenantID, messageID, recipient string) error {
	if u.baseURL() == "" {
		return fmt.Errorf("no base URL is configured, so no unsubscribe link can be built")
	}
	if u.trackingSigner == nil {
		return fmt.Errorf("no tracking signer is configured")
	}

	signature := u.trackingSigner.Sign(tracking.Link{
		Kind:      trackingKindUnsubscribe,
		TenantID:  tenantID,
		MessageID: messageID,
		Recipient: recipient,
	})

	url := fmt.Sprintf("%s/unsubscribe/%s/%s/%s?%s=%s",
		u.baseURL(), tenantID, messageID,
		base64.RawURLEncoding.EncodeToString([]byte(recipient)),
		tracking.SignatureParam, signature,
	)

	return msg.SetOneClickUnsubscribe(url)
}

func (u *sendEmailUsecase) injectPixel(htmlContent, tenantID, messageID, recipient, recipientEncoded string) string {
	signature := u.trackingSigner.Sign(tracking.Link{
		Kind:      trackingKindOpen,
		TenantID:  tenantID,
		MessageID: messageID,
		Recipient: recipient,
	})

	pixel := fmt.Sprintf(
		`<img src="%s/track/open/%s/%s/%s?%s=%s" width="1" height="1" style="display:none">`,
		u.baseURL(), tenantID, messageID, recipientEncoded, tracking.SignatureParam, signature,
	)

	if idx := strings.LastIndex(htmlContent, "</body>"); idx != -1 {
		return htmlContent[:idx] + pixel + htmlContent[idx:]
	}
	return htmlContent + pixel
}

func (u *sendEmailUsecase) rewriteLinks(htmlContent, tenantID, messageID, recipient, recipientEncoded string) string {
	return hrefRegexp.ReplaceAllStringFunc(htmlContent, func(match string) string {
		submatch := hrefRegexp.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}

		originalURL := html.UnescapeString(submatch[1])

		// Anything that is not an ordinary web link is left alone: anchors and
		// mailto: links have nothing to track, and other schemes must never be
		// reachable through the gateway's own domain.
		if tracking.ValidateTarget(originalURL) != nil {
			return match
		}

		signature := u.trackingSigner.Sign(tracking.Link{
			Kind:      trackingKindClick,
			TenantID:  tenantID,
			MessageID: messageID,
			Recipient: recipient,
			TargetURL: originalURL,
		})

		return fmt.Sprintf(`href="%s/track/click/%s/%s/%s?url=%s&amp;%s=%s"`,
			u.baseURL(), tenantID, messageID, recipientEncoded,
			url.QueryEscape(originalURL), tracking.SignatureParam, signature)
	})
}

func (u *sendEmailUsecase) RecordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient, subject, errorMessage string, metadata map[string]any) error {
	return u.eventUsecase.RecordEvent(ctx, tenantID, providerID, messageID, eventType, recipient, subject, errorMessage, metadata)
}

func (u *sendEmailUsecase) renderTemplate(ctx context.Context, tenantID string, req *panmailv1.SendEmailRequest) (string, string, string, error) {
	subject := req.Subject
	bodyHTML := ""
	bodyText := ""

	if req.TemplateId != "" {
		tpl, err := u.getTemplate(ctx, tenantID, req.TemplateId)
		if err != nil {
			return "", "", "", fmt.Errorf("failed to get template: %w", err)
		}
		if tpl == nil {
			return "", "", "", fmt.Errorf("template not found: %s", req.TemplateId)
		}

		data := make(map[string]any)
		if req.TemplateData != nil {
			data = req.TemplateData.AsMap()
		}

		var errSub, errHTML, errText error
		subject, errSub = u.renderer.Render(tpl.Subject, data, false)
		bodyHTML, errHTML = u.renderer.Render(tpl.BodyHTML, data, true)
		bodyText, errText = u.renderer.Render(tpl.BodyText, data, false)

		if errSub != nil {
			return "", "", "", fmt.Errorf("failed to render subject: %w", errSub)
		}
		if errHTML != nil {
			return "", "", "", fmt.Errorf("failed to render body_html: %w", errHTML)
		}
		if errText != nil {
			return "", "", "", fmt.Errorf("failed to render body_text: %w", errText)
		}
	} else {
		if req.BodyHtml != "" || req.BodyText != "" {
			bodyHTML = req.BodyHtml
			bodyText = req.BodyText
		} else {
			// Backwards compatibility for deprecated fields
			if req.IsHtml {
				bodyHTML = req.Body
			} else {
				bodyText = req.Body
			}
		}
	}

	return subject, bodyHTML, bodyText, nil
}

func (u *sendEmailUsecase) getTemplate(ctx context.Context, tenantID, templateID string) (*templateEntities.Template, error) {
	cacheKey := tenantID + ":" + templateID
	if tpl, ok := u.templateCache.Get(cacheKey); ok {
		return tpl, nil
	}

	tpl, err := u.templateRepo.GetByID(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	if tpl != nil {
		u.templateCache.Put(cacheKey, tpl)
	}
	return tpl, nil
}

func (u *sendEmailUsecase) getProviders(ctx context.Context, tenantID string) ([]*providerEntities.EmailProvider, error) {
	if providers, ok := u.providerCache.Get(tenantID); ok {
		return providers, nil
	}

	providers, _, err := u.providerRepo.List(ctx, tenantID, "", "", maxProvidersPerTenant, "")
	if err != nil {
		return nil, err
	}
	u.providerCache.Put(tenantID, providers)
	return providers, nil
}

func (u *sendEmailUsecase) getProvider(ctx context.Context, tenantID, providerID string) (*providerEntities.EmailProvider, error) {
	// Try to find in cached list first
	all, err := u.getProviders(ctx, tenantID)
	if err == nil {
		for _, p := range all {
			if p.ID == providerID {
				return p, nil
			}
		}
	}

	// Fallback to DB
	return u.providerRepo.GetByID(ctx, tenantID, providerID)
}

// suppressionsFor asks which of a message's recipients are on the tenant's
// suppression list, returning address -> reason for those that are.
//
// The address is normalised first, for the same reason the suppression usecase
// normalises on write: a bounce recorded for "Alice@Example.COM" has to stop
// the next send to "alice@example.com". Reading with the raw address made the
// list fail open on any difference of case or display name.
// The addresses must already be normalised: the caller holds that slice for
// its own lookups, and normalising is the expensive part.
func (u *sendEmailUsecase) suppressionsFor(
	ctx context.Context, tenantID string, normalised []string,
) (map[string]string, error) {
	found, err := u.suppressionRepo.GetByEmails(ctx, tenantID, normalised)
	if err != nil {
		return nil, err
	}

	// Reduced to address -> reason: the reason is all the send path needs, and
	// holding the rows would keep a row per recipient alive for the rest of
	// the request.
	reasons := make(map[string]string, len(found))
	for address, suppression := range found {
		if suppression != nil {
			reasons[address] = suppression.Reason
		}
	}
	return reasons, nil
}
