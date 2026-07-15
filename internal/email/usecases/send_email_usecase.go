package usecases

import (
	"context"
	"encoding/json"
	"fmt"
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

func (u *sendEmailUsecase) SendEmail(ctx context.Context, tenantID string, req *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error) {
	if req.From == "" {
		return nil, fmt.Errorf("from address is mandatory")
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

	// Check suppressions for each recipient
	for _, recipient := range req.To {
		isSuppressed, reason, err := u.isSuppressed(ctx, tenantID, recipient)
		if err != nil {
			return nil, err
		}
		if isSuppressed {
			u.recordEvent(ctx, tenantID, "", messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DROPPED, recipient, reason, nil)
			return nil, fmt.Errorf("recipient %s is suppressed: %s", recipient, reason)
		}
		// Record initial SENT event
		u.recordEvent(ctx, tenantID, "", messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, recipient, "", nil)
	}

	subject, bodyHTML, bodyText, err := u.renderTemplate(ctx, tenantID, req)
	if err != nil {
		return nil, err
	}

	// Inject tracking if enabled
	if bodyHTML != "" && u.baseURL != "" {
		bodyHTML = u.injectTracking(tenantID, messageID, bodyHTML)
	}

	// Save message content for analytics
	_ = u.eventUsecase.SaveMessage(ctx, &panmailv1.EmailMessage{
		Id:          messageID,
		TenantId:    tenantID,
		From:        req.From,
		To:          req.To,
		Subject:     subject,
		BodyHtml:    bodyHTML,
		BodyText:    bodyText,
		Attachments: req.Attachments,
	})

	// Save to outbox for the worker to pick up
	reqBytes, _ := json.Marshal(req)
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
	_ = u.outboxRepo.Create(ctx, outboxEmail)

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
		return nil, err
	}

	// Inject tracking if enabled
	if bodyHTML != "" && u.baseURL != "" {
		bodyHTML = u.injectTracking(tenantID, messageID, bodyHTML)
	}

	msg := gsmail.Email{
		From:     req.From,
		To:       req.To,
		Subject:  subject,
		Body:     []byte(bodyText),
		HTMLBody: []byte(bodyHTML),
	}

	for _, a := range req.Attachments {
		msg.Attachments = append(msg.Attachments, gsmail.Attachment{
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Data:        a.Content,
		})
	}

	var lastErr error
	for _, p := range providers {
		// Skip receivers
		if p.Type == panmailv1.ProviderType_PROVIDER_TYPE_IMAP || p.Type == panmailv1.ProviderType_PROVIDER_TYPE_POP3 {
			continue
		}

		senderObj, err := u.providerFactory.CreateSender(p)
		if err != nil {
			lastErr = err
			continue
		}

		sender, ok := senderObj.(gsmail.Sender)
		if !ok {
			continue
		}

		err = sender.Send(ctx, msg)
		if err == nil {
			for _, recipient := range req.To {
				u.recordEvent(ctx, tenantID, p.ID, messageID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, recipient, "", nil)
			}
			return &panmailv1.SendEmailResponse{
				MessageId: messageID,
				Status:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
			}, nil
		}
		lastErr = err
		fmt.Printf("Provider %s failed: %v. Trying next...\n", p.Name, err)
	}

	return nil, lastErr
}

var (
	hrefRegexp = regexp.MustCompile(`href="([^"]+)"`)
)

func (u *sendEmailUsecase) injectTracking(tenantID, messageID, html string) string {
	// 1. Add tracking pixel before </body>
	pixel := fmt.Sprintf(`<img src="%s/track/open/%s/%s" width="1" height="1" style="display:none">`, u.baseURL, tenantID, messageID)
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
		trackingURL := fmt.Sprintf(`href="%s/track/click/%s/%s?url=%s"`, u.baseURL, tenantID, messageID, encodedURL)
		return trackingURL
	})
}

func (u *sendEmailUsecase) recordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient string, errorMessage string, metadata map[string]any) {
	_ = u.eventUsecase.RecordEvent(ctx, tenantID, providerID, messageID, eventType, recipient, errorMessage, metadata)
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

	providers, _, err := u.providerRepo.List(ctx, tenantID, 1000, "")
	if err != nil {
		return nil, err
	}
	u.providerCache.Store(tenantID, cacheEntry{data: providers, createdAt: time.Now()})
	return providers, nil
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
