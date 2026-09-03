package services

import (
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/emailfilter"
)

// webhookTrigger is the gateway's durable webhook worker, as much of it as a
// quarantine notification needs.
type webhookTrigger interface {
	Enqueue(tenantID string, event panmailv1.WebhookTriggerEvent, payload any)
}

// quarantineNotifier adapts the webhook worker to
// emailfilter.QuarantineNotifier.
//
// The adapter exists so the filter package never names the generated enum. It
// describes what happened; which wire event carries that is the API layer's
// business, and keeping the two apart is what lets the filter engine be tested
// without the API package at all.
//
// This file is therefore the single place the four moments are mapped onto
// four wire events. A reader checking that a release does not go out as a
// rejection has one place to look.
type quarantineNotifier struct {
	trigger webhookTrigger
}

// NewQuarantineNotifier wires the quarantine lifecycle to the tenant's webhook
// subscriptions.
func NewQuarantineNotifier(trigger webhookTrigger) emailfilter.QuarantineNotifier {
	if trigger == nil {
		return nil
	}
	return &quarantineNotifier{trigger: trigger}
}

func (n *quarantineNotifier) NotifyHeld(tenantID string, event emailfilter.HeldEvent) {
	n.trigger.Enqueue(tenantID, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_HELD, event)
}

func (n *quarantineNotifier) NotifyReleased(tenantID string, event emailfilter.OutcomeEvent) {
	n.trigger.Enqueue(tenantID, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_RELEASED, event)
}

// NotifyRejected sends MAIL_QUARANTINE_REJECTED, not MAIL_REJECTED. The latter
// means a provider refused a send and is published by the delivery pipeline;
// sending it here would tell a subscriber a remote server did something a
// person did.
func (n *quarantineNotifier) NotifyRejected(tenantID string, event emailfilter.OutcomeEvent) {
	n.trigger.Enqueue(tenantID, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_QUARANTINE_REJECTED, event)
}

func (n *quarantineNotifier) NotifyExpired(tenantID string, event emailfilter.OutcomeEvent) {
	n.trigger.Enqueue(tenantID, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_EXPIRED, event)
}
