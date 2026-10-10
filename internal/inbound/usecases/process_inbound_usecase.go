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
	"github.com/gsoultan/panmail/pkg/emailutil"
	"github.com/gsoultan/panmail/pkg/tracking"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type inboundUsecase struct {
	repo           stores.InboundRepository
	eventUsecase   eventusecases.ProcessEventUsecase
	webhookTrigger WebhookTrigger

	// screener is optional. A nil one accepts every message, which is what a
	// gateway with no inbound rules configured must do.
	screener emailfilter.Screener

	// bounceSigner verifies the reference a delivery report quotes back. Nil
	// means no report is believed; see recordBounce.
	bounceSigner *tracking.Signer
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

// WithBounceSigner installs the key bounce references are verified with: the
// tracking signer the send path minted them with.
func (u *inboundUsecase) WithBounceSigner(s *tracking.Signer) InboundUsecase {
	u.bounceSigner = s
	return u
}

func (u *inboundUsecase) Process(ctx context.Context, email *panmailv1.InboundEmail, report *DeliveryReport) error {
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

	// Bounce detection. Only a report the transport found to be a delivery
	// status notification is considered, and only what it can be tied to a
	// message panmail sent is recorded; see recordBounce.
	u.recordBounce(ctx, email, report)

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

// recordBounce records what a delivery report says about the recipients it
// names, for those it can be tied to a copy panmail actually sent.
//
// A recorded hard bounce suppresses the address for good, and the mailbox
// these reports arrive in accepts mail from anyone. This used to decide on the
// subject line and a few phrases in the body, so a plain message saying "user
// unknown" with a Final-Recipient line suppressed whichever address it named.
// Now a report counts only when both of these hold:
//
//   - it is structurally a delivery status notification: the receiving
//     transport found a message/delivery-status part (see ParseDeliveryReport);
//   - the original headers it hands back carry the X-Panmail-Ref panmail put on
//     the copy, and that reference verifies for this tenant and for the
//     address the report names, and the event store shows that message was
//     handed to a provider for that address.
//
// The reference is signed per tenant, message and recipient, so another
// tenant's message, an address the message never went to, or another
// recipient of the same message all fail. Anything that fails is not recorded;
// the message itself is still stored as inbound mail by the caller.
func (u *inboundUsecase) recordBounce(ctx context.Context, email *panmailv1.InboundEmail, report *DeliveryReport) {
	if report == nil {
		return
	}
	if u.bounceSigner == nil || !u.bounceSigner.HasKey() {
		slog.Warn("cannot verify a delivery report without the tracking key; not recording it",
			"id", email.Id, "tenant_id", email.TenantId)
		return
	}

	refs := report.ReturnedHeaders.Values(tracking.BounceRefHeader)
	if len(refs) == 0 {
		slog.Info("delivery report quotes no panmail reference; not recording it",
			"id", email.Id, "tenant_id", email.TenantId)
		return
	}

	for _, rcpt := range report.Recipients {
		eventType, ok := reportedEventType(rcpt)
		if !ok {
			continue
		}

		messageID := ""
		for _, ref := range refs {
			if id, err := u.bounceSigner.VerifyBounceRef(ref, email.TenantId, rcpt.Address); err == nil {
				messageID = id
				break
			}
		}
		if messageID == "" {
			slog.Warn("delivery report names an address its reference does not cover; not recording it",
				"id", email.Id, "tenant_id", email.TenantId)
			continue
		}

		if !u.wasSentTo(ctx, email.TenantId, messageID, rcpt.Address) {
			slog.Warn("delivery report refers to a message not sent to the address it names; not recording it",
				"id", email.Id, "tenant_id", email.TenantId, "message_id", messageID)
			continue
		}

		metadata := map[string]any{
			"bounce_subject": email.Subject,
			"inbound_id":     email.Id,
			"dsn_status":     rcpt.Status,
		}
		_ = u.eventUsecase.RecordEvent(ctx, email.TenantId, "", messageID, eventType, rcpt.Address, "", rcpt.Diagnostic, metadata)
	}
}

// wasSentTo reports whether the event store shows messageID handed to a
// provider for address in this tenant. A store that cannot be read counts as
// no: a bounce lost to an outage costs one more attempt later, a bounce
// believed wrongly costs the address for good.
func (u *inboundUsecase) wasSentTo(ctx context.Context, tenantID, messageID, address string) bool {
	events, err := u.eventUsecase.ListByMessageID(ctx, tenantID, messageID)
	if err != nil {
		slog.Error("failed to read a message's events to verify a delivery report",
			"error", err, "tenant_id", tenantID, "message_id", messageID)
		return false
	}
	for _, e := range events {
		switch e.Type {
		case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED:
			if e.TenantId == tenantID && bareAddress(e.Recipient) == address {
				return true
			}
		}
	}
	return false
}

// reportedEventType reads what one recipient section says happened. ok is
// false for a section that reports a success (delivered, relayed, expanded),
// which is not a bounce at all.
func reportedEventType(r ReportedRecipient) (panmailv1.EmailEventType, bool) {
	switch r.Action {
	case "delivered", "relayed", "expanded":
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED, false
	}

	if r.Action == "delayed" || strings.HasPrefix(r.Status, "4") {
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE, true
	}
	if r.Action != "failed" && !strings.HasPrefix(r.Status, "5") {
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED, true
	}

	// The status code first: it is the field the report exists to carry. The
	// diagnostic is free text from the remote server and is read with the
	// same classifier the send path uses on a synchronous refusal. Anything
	// beyond hard, soft or rejected -- a complaint or an unsubscribe read out
	// of a diagnostic -- is not something a delivery report can say.
	if emailutil.ClassifyError(r.Status).Type == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE {
		return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, true
	}
	switch c := emailutil.ClassifyError(strings.TrimSpace(r.Diagnostic + " " + r.Status)); c.Type {
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE,
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED:
		return c.Type, true
	}
	return panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED, true
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
