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

	// Lowered once, from a bounded prefix. This used to lower the whole of
	// text+HTML afresh for each of four phrases, so a 25 MiB message that
	// happened to have a bounce-like subject cost around 200 MiB of garbage.
	// A report puts its status lines near the top; nothing past the prefix
	// was going to change the verdict.
	scan := bounceScanText(email.BodyText, email.BodyHtml)

	// A simplified heuristic, deliberately: the classification only decides
	// hard against soft, and the recipient below decides whether anything is
	// recorded at all.
	eventType := panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED
	if strings.Contains(scan, "5.1.1") || strings.Contains(scan, "user unknown") {
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE
	} else if strings.Contains(scan, "mailbox full") || strings.Contains(scan, "4.2.2") {
		eventType = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE
	}

	// The bounced address comes from the report itself and from nowhere else.
	//
	// This used to fall back to the message's From when no Final-Recipient was
	// found, and From is whoever sent the bounce -- usually MAILER-DAEMON, but
	// on anything that is not a real mail server's report it is an address the
	// sender chose. A hard bounce suppresses permanently, so the fallback let
	// any message with the right subject and wording suppress any address the
	// sender liked. With no recipient there is nothing to attribute the bounce
	// to, and recording nothing is the honest outcome.
	recipient := dsnRecipient(scan)
	if recipient == "" {
		slog.Debug("inbound bounce names no recipient; not recording it",
			"id", email.Id, "tenant_id", email.TenantId)
		return
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

// maxBounceScan bounds how much of a message bounce detection reads. A
// delivery report is a few kilobytes; the bound is generous for one with the
// original message quoted above its status lines.
const maxBounceScan = 256 << 10

// bounceScanText returns the lower-cased start of a message's text and HTML
// parts, at most maxBounceScan bytes of the two together, text first.
func bounceScanText(text, html string) string {
	if len(text) > maxBounceScan {
		text = text[:maxBounceScan]
	}
	if room := maxBounceScan - len(text); len(html) > room {
		html = html[:room]
	}
	return strings.ToLower(text + "\n" + html)
}

// dsnRecipient returns the address a delivery status notification reports on,
// or "" when it names none. It expects text already lower-cased, as
// bounceScanText returns it; the address comes back lower-cased too, which is
// how the event store keys a recipient anyway.
//
// RFC 3464 writes the field as `Final-Recipient: rfc822; user@example.com` --
// an address type, a semicolon, then usually a space before the address. The
// parser this replaces cut at the first whitespace after the semicolon, which
// in the common spelling is immediately, so every real report yielded an empty
// recipient. Original-Recipient is the fallback for a report that carries only
// that.
func dsnRecipient(body string) string {
	for _, field := range []string{"final-recipient:", "original-recipient:"} {
		if r := dsnField(body, field); r != "" {
			return r
		}
	}
	return ""
}

func dsnField(body, field string) string {
	from := 0
	for {
		idx := strings.Index(body[from:], field)
		if idx < 0 {
			return ""
		}
		start := from + idx + len(field)
		from = start

		value := body[start:]
		if end := strings.IndexAny(value, "\r\n"); end >= 0 {
			value = value[:end]
		}
		// The address type is optional in practice even though the RFC
		// requires it, so a value with no semicolon is taken as the address.
		if _, after, found := strings.Cut(value, ";"); found {
			value = after
		}
		value = strings.TrimSpace(value)
		// Some servers bracket the address. Past it, an HTML part carries the
		// line on with markup and a text one may carry a trailing comment;
		// neither is part of the address.
		if rest, ok := strings.CutPrefix(value, "<"); ok {
			value, _, _ = strings.Cut(rest, ">")
		}
		if end := strings.IndexAny(value, " \t<"); end >= 0 {
			value = value[:end]
		}
		if strings.Contains(value, "@") {
			return value
		}
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
