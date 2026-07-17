package usecases

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/email/repositories/stores"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
	suppressionStores "github.com/gsoultan/panmail/internal/suppression/repositories/stores"
	templateEntities "github.com/gsoultan/panmail/internal/template/repositories/entities"
	templateStores "github.com/gsoultan/panmail/internal/template/repositories/stores"
	"github.com/gsoultan/panmail/pkg/emailutil"
	"google.golang.org/protobuf/encoding/protojson"
)

type sendEmailUsecase struct {
	providerRepo    providerStores.Repository
	templateRepo    templateStores.TemplateRepository
	suppressionRepo suppressionStores.SuppressionRepository
	outboxRepo      stores.OutboxRepository
	eventUsecase    eventusecases.ProcessEventUsecase
	providerFactory providerEntities.ProviderFactory
	renderer        TemplateRenderer
	baseURL         string
	queueWorker     QueueWorker

	providerCache sync.Map
	templateCache sync.Map
}

func NewSendEmailUsecase(
	repo providerStores.Repository,
	templateRepo templateStores.TemplateRepository,
	suppressionRepo suppressionStores.SuppressionRepository,
	outboxRepo stores.OutboxRepository,
	eventUsecase eventusecases.ProcessEventUsecase,
	factory providerEntities.ProviderFactory,
	renderer TemplateRenderer,
	baseURL string,
) SendEmailUsecase {
	return &sendEmailUsecase{
		providerRepo:    repo,
		templateRepo:    templateRepo,
		suppressionRepo: suppressionRepo,
		outboxRepo:      outboxRepo,
		eventUsecase:    eventUsecase,
		providerFactory: factory,
		renderer:        renderer,
		baseURL:         baseURL,
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

	// Extract domain from From address
	fromDomain := emailutil.GetRootDomain(req.From)

	// Fetch provider to validate domain
	provider, err := u.getProvider(ctx, tenantID, req.ProviderId)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}
	if provider == nil {
		return nil, fmt.Errorf("provider not found: %s", req.ProviderId)
	}

	// Extract domain from provider host
	var host string
	switch provider.Type {
	case panmailv1.ProviderType_PROVIDER_TYPE_SMTP:
		c := &panmailv1.SmtpConfig{}
		if err := protojson.Unmarshal(provider.Config, c); err == nil {
			host = c.Host
		}
	}

	if host != "" {
		hostDomain := emailutil.GetRootDomain(host)
		if fromDomain != hostDomain {
			return nil, fmt.Errorf("domain mismatch: from address domain (%s) does not match provider host domain (%s)", fromDomain, hostDomain)
		}
	}

	// 1. Worker mode (Actual Sending)
	if skip, ok := ctx.Value(SkipOutboxKey).(bool); ok && skip {
		messageID, _ := ctx.Value(MessageIDKey).(string)
		return u.doSend(ctx, tenantID, req, messageID)
	}

	// 2. Client mode (Async Queuing)
	messageID := uuid.New().String()

	// Extract domain from From address
	fromParts := strings.Split(req.From, "@")
	if len(fromParts) != 2 {
		return nil, fmt.Errorf("invalid from address: %s", req.From)
	}

	// Collect unique recipients to avoid duplication
	recipientSet := make(map[string]struct{})
	var allRecipients []string
	addRecipients := func(emails []string) {
		for _, email := range emails {
			email = strings.ToLower(strings.TrimSpace(email))
			if email == "" {
				continue
			}
			if _, ok := recipientSet[email]; !ok {
				recipientSet[email] = struct{}{}
				allRecipients = append(allRecipients, email)
			}
		}
	}
	addRecipients(req.To)
	addRecipients(req.Cc)
	addRecipients(req.Bcc)

	// Check suppressions for each recipient
	for _, recipient := range allRecipients {
		isSuppressed, reason, err := u.isSuppressed(ctx, tenantID, recipient)
		if err != nil {
			slog.Error("failed to check suppression", "error", err, "recipient", recipient)
			return nil, err
		}
		if isSuppressed {
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
		Status:      entities.OutboxStatusPending,
		RetryCount:  0,
		NextRetryAt: time.Now(), // Try immediately
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := u.outboxRepo.Create(ctx, outboxEmail); err != nil {
		slog.Error("failed to create outbox email", "error", err, "id", messageID)
		return nil, fmt.Errorf("failed to enqueue email: %w", err)
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

	// Collect unique recipients to avoid duplication
	recipientSet := make(map[string]struct{})
	var uniqueRecipients []string
	addRecipients := func(emails []string) {
		for _, email := range emails {
			email = strings.ToLower(strings.TrimSpace(email))
			if email == "" {
				continue
			}
			if _, ok := recipientSet[email]; !ok {
				recipientSet[email] = struct{}{}
				uniqueRecipients = append(uniqueRecipients, email)
			}
		}
	}
	addRecipients(req.To)
	addRecipients(req.Cc)
	addRecipients(req.Bcc)

	slog.Info("starting actual delivery", "id", messageID, "recipient_count", len(uniqueRecipients), "provider_count", len(providers))

	// We iterate through recipients to support individual tracking and status
	var deliveryErrors []error
	for _, recipient := range uniqueRecipients {
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

			senderObj, err := u.providerFactory.CreateSender(p)
			if err != nil {
				slog.Error("failed to create sender for provider", "error", err, "provider", p.Name)
				recipientErr = err
				continue
			}

			sender, ok := senderObj.(gsmail.Sender)
			if !ok {
				continue
			}

			// Inject tracking per recipient
			currentBodyHTML := bodyHTML
			if currentBodyHTML != "" && u.baseURL != "" {
				currentBodyHTML = u.injectTracking(tenantID, messageID, recipient, currentBodyHTML)
			}

			msg := gsmail.Email{
				From:     req.From,
				To:       []string{recipient},
				Subject:  subject,
				Body:     []byte(bodyText),
				HTMLBody: []byte(currentBodyHTML),
			}

			for _, a := range req.Attachments {
				msg.Attachments = append(msg.Attachments, gsmail.Attachment{
					Filename:    a.Filename,
					ContentType: a.ContentType,
					Data:        a.Content,
				})
			}

			// Record SENT event (actually handing over to provider)
			_ = u.RecordEvent(ctx, tenantID, p.ID, messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, recipient, subject, "", nil)

			err = sender.Send(ctx, msg)
			if err == nil {
				slog.Info("email delivered successfully", "id", messageID, "provider", p.Name, "recipient", recipient)
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

	if len(deliveryErrors) > 0 && len(deliveryErrors) == len(uniqueRecipients) {
		// All recipients failed
		return nil, errors.Join(deliveryErrors...)
	}

	return &panmailv1.SendEmailResponse{
		MessageId: messageID,
		Status:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
	}, nil
}

var (
	hrefRegexp = regexp.MustCompile(`href="([^"]+)"`)
)

func (u *sendEmailUsecase) injectTracking(tenantID, messageID, recipient, html string) string {
	recipientEncoded := base64.RawURLEncoding.EncodeToString([]byte(recipient))
	// 1. Add tracking pixel before </body>
	pixel := fmt.Sprintf(`<img src="%s/track/open/%s/%s/%s" width="1" height="1" style="display:none">`, u.baseURL, tenantID, messageID, recipientEncoded)
	if idx := strings.LastIndex(html, "</body>"); idx != -1 {
		html = html[:idx] + pixel + html[idx:]
	} else {
		html = html + pixel
	}

	// 2. Wrap links
	return hrefRegexp.ReplaceAllStringFunc(html, func(match string) string {
		submatch := hrefRegexp.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		originalURL := submatch[1]
		if strings.HasPrefix(originalURL, "mailto:") || strings.HasPrefix(originalURL, "#") {
			return match
		}

		encodedURL := url.QueryEscape(originalURL)
		trackingURL := fmt.Sprintf(`href="%s/track/click/%s/%s/%s?url=%s"`, u.baseURL, tenantID, messageID, recipientEncoded, encodedURL)
		return trackingURL
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

type cacheEntry struct {
	data      any
	createdAt time.Time
}

const cacheTTL = 1 * time.Minute

func (u *sendEmailUsecase) getTemplate(ctx context.Context, tenantID, templateID string) (*templateEntities.Template, error) {
	cacheKey := tenantID + ":" + templateID
	if val, ok := u.templateCache.Load(cacheKey); ok {
		entry := val.(cacheEntry)
		if time.Since(entry.createdAt) < cacheTTL {
			return entry.data.(*templateEntities.Template), nil
		}
	}

	tpl, err := u.templateRepo.GetByID(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	if tpl != nil {
		u.templateCache.Store(cacheKey, cacheEntry{data: tpl, createdAt: time.Now()})
	}
	return tpl, nil
}

func (u *sendEmailUsecase) getProviders(ctx context.Context, tenantID string) ([]*providerEntities.EmailProvider, error) {
	if val, ok := u.providerCache.Load(tenantID); ok {
		entry := val.(cacheEntry)
		if time.Since(entry.createdAt) < cacheTTL {
			return entry.data.([]*providerEntities.EmailProvider), nil
		}
	}

	providers, _, err := u.providerRepo.List(ctx, tenantID, "", "", 1000, "")
	if err != nil {
		return nil, err
	}
	u.providerCache.Store(tenantID, cacheEntry{data: providers, createdAt: time.Now()})
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

func (u *sendEmailUsecase) isSuppressed(ctx context.Context, tenantID, email string) (bool, string, error) {
	s, err := u.suppressionRepo.GetByEmail(ctx, tenantID, email)
	if err != nil {
		return false, "", err
	}
	if s == nil {
		return false, "", nil
	}
	return true, s.Reason, nil
}
