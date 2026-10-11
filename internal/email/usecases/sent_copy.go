package usecases

import (
	"context"
	"log/slog"

	"github.com/gsoultan/gsmail"
	"google.golang.org/protobuf/proto"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// queueSentCopy hands an accepted message to the Sent copies queue when its
// sender asked for one.
//
// A failure is logged, not returned: the message is already queued for
// delivery, and refusing it now over the copy would tell the client a send
// failed that is going to happen anyway.
func (u *sendEmailUsecase) queueSentCopy(
	ctx context.Context, tenantID, messageID string,
	req *panmailv1.SendEmailRequest, subject, bodyHTML, bodyText string,
) {
	if u.sentCopies == nil || !req.GetSaveSentCopy() {
		return
	}

	// The copy is of the message as sent, so the template is rendered into it
	// here rather than again later, when the template may have changed.
	rendered := proto.CloneOf(req)
	rendered.Subject = subject
	rendered.BodyHtml = bodyHTML
	rendered.BodyText = bodyText
	rendered.TemplateId = ""
	rendered.TemplateData = nil
	rendered.Body = ""
	rendered.IsHtml = false
	rendered.IdempotencyKey = ""

	if err := u.sentCopies.Enqueue(ctx, tenantID, messageID, rendered); err != nil {
		slog.Error("failed to queue a Sent copy", "error", err, "id", messageID, "tenant_id", tenantID)
	}
}

// SentCopyEmail builds the message a Sent copy files: what every recipient
// was sent, with the full visible audience in the headers and none of the
// per-recipient tracking, unsubscribe link or bounce reference.
func SentCopyEmail(req *panmailv1.SendEmailRequest) gsmail.Email {
	msg := gsmail.Email{
		From:     req.GetFrom(),
		To:       req.GetTo(),
		Cc:       req.GetCc(),
		Subject:  req.GetSubject(),
		Body:     []byte(req.GetBodyText()),
		HTMLBody: []byte(req.GetBodyHtml()),
	}
	applyReplyHeaders(&msg, req)
	for _, a := range req.GetAttachments() {
		msg.Attachments = append(msg.Attachments, gsmail.Attachment{
			Filename:    a.GetFilename(),
			ContentType: a.GetContentType(),
			ContentID:   a.GetContentId(),
			Data:        a.GetContent(),
		})
	}
	return msg
}
