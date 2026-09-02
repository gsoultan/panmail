package services

import (
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/emailfilter"
)

// webhookTrigger is the gateway's durable webhook worker, as much of it as a
// held notification needs.
type webhookTrigger interface {
	Enqueue(tenantID string, event panmailv1.WebhookTriggerEvent, payload any)
}

// heldNotifier adapts the webhook worker to emailfilter.HeldNotifier.
//
// The adapter exists so the filter package never names the generated enum. It
// describes what happened; which wire event carries that is the API layer's
// business, and keeping the two apart is what lets the filter engine be tested
// without the API package at all.
type heldNotifier struct {
	trigger webhookTrigger
}

// NewHeldNotifier wires held messages to the tenant's webhook subscriptions.
func NewHeldNotifier(trigger webhookTrigger) emailfilter.HeldNotifier {
	if trigger == nil {
		return nil
	}
	return &heldNotifier{trigger: trigger}
}

func (n *heldNotifier) NotifyHeld(tenantID string, event emailfilter.HeldEvent) {
	n.trigger.Enqueue(tenantID, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_HELD, event)
}
