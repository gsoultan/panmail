package usecases

import (
	"context"
	"log/slog"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// dropSuppressedAtDelivery removes recipients suppressed since the message was
// admitted, recording each as DROPPED, and returns the set it removed.
//
// Admission refuses a whole message when any recipient is suppressed, because
// the caller is still waiting and can take the address off and send again.
// Delivery cannot do that: the message was accepted with a 200 long ago, the
// caller has moved on, and the other recipients still want it. So a recipient
// suppressed in between is dropped on its own and the rest are delivered.
//
// Nothing is returned as an error for a dropped recipient, which is also why
// no retry can follow one. A failed lookup is different: it is returned, so
// the worker tries again later. Failing closed is the only choice that honours
// an unsubscribe -- sending because the list could not be read would mail
// exactly the person who asked to be left alone.
func (u *sendEmailUsecase) dropSuppressedAtDelivery(
	ctx context.Context,
	tenantID, messageID, subject string,
	recipients []string,
	delivered map[string]bool,
) (map[string]bool, error) {
	if u.suppressionRepo == nil {
		return nil, nil
	}

	// Keyed as admission keys them, via suppressionKeyOf. The recipients here
	// only went through uniqueRecipients, which lowercases but keeps a display
	// name, and a suppression is never stored under one.
	keyOf := make(map[string]string, len(recipients))
	keys := make([]string, 0, len(recipients))
	for _, r := range recipients {
		if delivered[r] {
			continue
		}
		k := suppressionKeyOf(r)
		keyOf[r] = k
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil, nil
	}

	suppressed, err := u.suppressionsFor(ctx, tenantID, keys)
	if err != nil {
		slog.Error("failed to check suppression at delivery", "error", err, "id", messageID)
		return nil, err
	}
	if len(suppressed) == 0 {
		return nil, nil
	}

	dropped := make(map[string]bool, len(suppressed))
	for r, k := range keyOf {
		reason, ok := suppressed[k]
		if !ok {
			continue
		}
		dropped[r] = true
		slog.Info("dropping a recipient suppressed since admission",
			"id", messageID, "recipient", r, "reason", reason)
		// DROPPED, as admission records it, so the tenant's outbound webhook
		// fires MAIL_REJECTED for this recipient rather than silence.
		_ = u.RecordEvent(ctx, tenantID, "", messageID,
			panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DROPPED, r, subject, reason, nil)
	}
	return dropped, nil
}
