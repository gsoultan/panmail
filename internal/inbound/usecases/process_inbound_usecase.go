package usecases

import (
	"context"
	"log/slog"
	"strings"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/emailfilter"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type inboundUsecase struct {
	repo           stores.InboundRepository
	eventUsecase   eventusecases.ProcessEventUsecase
	webhookTrigger WebhookTrigger

	// screener is optional. A nil one accepts every message, which is what a
	// gateway with no inbound rules configured must do.
	screener emailfilter.Screener
}

func NewInboundUsecase(repo stores.InboundRepository, eventUsecase eventusecases.ProcessEventUsecase, webhookTrigger WebhookTrigger) InboundUsecase {
	return &inboundUsecase{
		repo:           repo,
		eventUsecase:   eventUsecase,
		webhookTrigger: webhookTrigger,
	}
}

// WithScreener turns on inbound filtering. Separate from the constructor so
// every existing caller keeps working unchanged and unfiltered.
func (u *inboundUsecase) WithScreener(s emailfilter.Screener) InboundUsecase {
	u.screener = s
	return u
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

	// A message already seen is skipped whole, not merely not-rewritten.
	//
	// The poller re-reads the newest messages every tick, so the same email
	// arrives here again and again. Storing it twice was the visible symptom;
	// the expensive ones were downstream — bounce detection recording a bounce
	// again, and the tenant's webhook firing again for a message that arrived
	// once. Anyone consuming that endpoint saw the same delivery failure
	// reported every thirty seconds for as long as the message sat in the
	// mailbox.
	//
	// A lookup that fails is treated as not-seen: refusing to accept mail
	// because the store could not be read would lose it outright, whereas
	// processing it twice is recoverable.
	if email.Id != "" {
		if existing, lookupErr := u.repo.GetByID(ctx, email.TenantId, email.Id); lookupErr == nil && existing != nil {
			return nil
		}
	}

	// Basic Bounce Detection
	u.detectAndRecordBounce(ctx, email)

	// Filter rules run after de-duplication and before anything is surfaced.
	//
	// After de-duplication because the poller re-reads the same messages every
	// tick, and screening a message the second time would record a second
	// review-queue entry for one arrival.
	if u.screener != nil {
		screened := emailfilter.Message{
			From:    email.From,
			To:      email.To,
			Subject: email.Subject,
			HTML:    email.BodyHtml,
			Text:    email.BodyText,
			Headers: singleValueHeaders(email.Headers),
			Size:    int64(len(email.BodyHtml) + len(email.BodyText) + len(email.Subject)),
		}

		decision, err := u.screener.Screen(ctx, email.TenantId, emailfilter.DirectionInbound, screened)
		if err != nil {
			// Inbound fails open, which is the opposite of the send path and
			// deliberate. A message refused here is not retried by the sender
			// — it has already been accepted over SMTP — so dropping it
			// because a database was briefly unreachable would lose mail
			// outright. Delivering something a rule would have held is
			// recoverable; losing it is not.
			slog.Error("failed to screen inbound message; delivering it",
				"error", err, "id", email.Id, "tenant_id", email.TenantId)
		} else if decision.Action != "" {
			record := emailfilter.NewFilteredMessage(email.TenantId, emailfilter.DirectionInbound, screened, decision)
			record.MessageID = email.Id
			record.PayloadRef = email.Id

			switch decision.Action {
			case emailfilter.ActionReject:
				// Recorded and dropped. No error: the message was accepted
				// over SMTP already, and returning one would make the poller
				// offer it again on every tick forever.
				if err := u.screener.Record(ctx, &record); err != nil {
					slog.Error("failed to record a rejected inbound message", "error", err, "id", email.Id)
				}
				return nil

			case emailfilter.ActionHold:
				// Stored, but not announced. Persisting anyway is the point:
				// the mail has arrived and losing it would be worse than
				// showing it to a reviewer. What the hold suppresses is the
				// webhook, so nothing downstream acts on it until someone
				// releases it.
				if writeErr := u.repo.Write(ctx, e); writeErr != nil {
					return writeErr
				}
				if err := u.screener.Record(ctx, &record); err != nil {
					slog.Error("failed to record a held inbound message", "error", err, "id", email.Id)
				}
				slog.Info("inbound email held for review",
					"id", email.Id, "tenant_id", email.TenantId, "rule", record.RuleName)
				return nil

			case emailfilter.ActionTag:
				if err := u.screener.Record(ctx, &record); err != nil {
					slog.Error("failed to record a tagged inbound message", "error", err, "id", email.Id)
				}
			}
		}
	}

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

// singleValueHeaders adapts the inbound map, which carries one value per
// header, to the multi-value shape rules are written against. A header may
// legitimately repeat — Received always does — but the inbound representation
// upstream of here has already collapsed them, and inventing values it does
// not have would be worse than working with what arrived.
func singleValueHeaders(in map[string]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for name, value := range in {
		out[strings.ToLower(strings.TrimSpace(name))] = []string{value}
	}
	return out
}
