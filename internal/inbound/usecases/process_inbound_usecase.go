package usecases

import (
	"context"
	"strings"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type inboundUsecase struct {
	repo           stores.InboundRepository
	eventUsecase   eventusecases.ProcessEventUsecase
	webhookTrigger WebhookTrigger
}

func NewInboundUsecase(repo stores.InboundRepository, eventUsecase eventusecases.ProcessEventUsecase, webhookTrigger WebhookTrigger) InboundUsecase {
	return &inboundUsecase{
		repo:           repo,
		eventUsecase:   eventUsecase,
		webhookTrigger: webhookTrigger,
	}
}

func (u *inboundUsecase) Process(ctx context.Context, email *panmailv1.InboundEmail) error {
	ts := time.Now()
	if email.Timestamp != nil {
		ts = email.Timestamp.AsTime()
	}

	e := &entities.InboundEmail{
		ID:        email.Id,
		TenantID:  email.TenantId,
		From:      email.From,
		To:        email.To,
		Subject:   email.Subject,
		BodyHTML:  email.BodyHtml,
		BodyText:  email.BodyText,
		Timestamp: ts,
		Headers:   email.Headers,
	}

	// Basic Bounce Detection
	u.detectAndRecordBounce(ctx, email)

	err := u.repo.Write(ctx, e)
	if err == nil && u.webhookTrigger != nil {
		u.webhookTrigger.Enqueue(email.TenantId, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_INBOUND, email)
	}

	return err
}

func (u *inboundUsecase) detectAndRecordBounce(ctx context.Context, email *panmailv1.InboundEmail) {
	subject := strings.ToLower(email.Subject)
	isBounce := strings.Contains(subject, "delivery status notification") ||
		strings.Contains(subject, "undeliverable") ||
		strings.Contains(subject, "returned mail") ||
		strings.Contains(subject, "failure notice")

	if !isBounce {
		return
	}

	// Try to find original recipient and type of bounce
	// This is a simplified heuristic
	eventType := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED
	if strings.Contains(strings.ToLower(email.BodyText+email.BodyHtml), "5.1.1") ||
		strings.Contains(strings.ToLower(email.BodyText+email.BodyHtml), "user unknown") {
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE
	} else if strings.Contains(strings.ToLower(email.BodyText+email.BodyHtml), "mailbox full") ||
		strings.Contains(strings.ToLower(email.BodyText+email.BodyHtml), "4.2.2") {
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE
	}

	// Try a very simple extraction of recipient from body if possible
	// (e.g. "Final-Recipient: rfc822; user@example.com")
	recipient := email.From
	body := email.BodyText + email.BodyHtml
	if idx := strings.Index(body, "Final-Recipient: rfc822;"); idx != -1 {
		rest := body[idx+len("Final-Recipient: rfc822;"):]
		if endIdx := strings.IndexAny(rest, " \n\r\t"); endIdx != -1 {
			recipient = strings.TrimSpace(rest[:endIdx])
		}
	}

	metadata := map[string]any{
		"bounce_subject": email.Subject,
		"inbound_id":     email.Id,
	}

	_ = u.eventUsecase.RecordEvent(ctx, email.TenantId, "", "", eventType, recipient, "", "", metadata)
}

func (u *inboundUsecase) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.InboundEmail, string, error) {
	emails, nextPageToken, err := u.repo.List(ctx, tenantID, pageSize, pageToken)
	if err != nil {
		return nil, "", err
	}

	var res []*panmailv1.InboundEmail
	for _, e := range emails {
		res = append(res, u.toProto(e))
	}

	return res, nextPageToken, nil
}

func (u *inboundUsecase) Get(ctx context.Context, tenantID, id string) (*panmailv1.InboundEmail, error) {
	e, err := u.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	return u.toProto(e), nil
}

func (u *inboundUsecase) toProto(e *entities.InboundEmail) *panmailv1.InboundEmail {
	return &panmailv1.InboundEmail{
		Id:        e.ID,
		TenantId:  e.TenantID,
		From:      e.From,
		To:        e.To,
		Subject:   e.Subject,
		BodyHtml:  e.BodyHTML,
		BodyText:  e.BodyText,
		Timestamp: timestamppb.New(e.Timestamp),
		Headers:   e.Headers,
	}
}
