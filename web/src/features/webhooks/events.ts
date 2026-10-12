import { WebhookTriggerEvent } from './services/webhook';

export const triggerEventOptions = [
  { value: WebhookTriggerEvent.MAIL_SENT.toString(), label: 'Mail Sent' },
  { value: WebhookTriggerEvent.MAIL_DELIVERED.toString(), label: 'Mail Delivered' },
  { value: WebhookTriggerEvent.MAIL_OPENED.toString(), label: 'Mail Opened' },
  { value: WebhookTriggerEvent.MAIL_CLICKED.toString(), label: 'Mail Clicked' },
  { value: WebhookTriggerEvent.MAIL_BOUNCED.toString(), label: 'Mail Bounced' },
  { value: WebhookTriggerEvent.MAIL_REJECTED.toString(), label: 'Mail Rejected' },
  { value: WebhookTriggerEvent.MAIL_INBOUND.toString(), label: 'Mail Inbound' },

  // The quarantine lifecycle. MAIL_HELD existed on the wire from the release
  // that added filtering but never appeared here, so the event fired and
  // nobody could subscribe to it.
  //
  // "Quarantine Rejected" is spelled out because "Mail Rejected" above is a
  // different thing — a provider refusing a send. Two options both reading
  // "Rejected" in one list is how someone subscribes to the wrong one.
  { value: WebhookTriggerEvent.MAIL_HELD.toString(), label: 'Mail Held for Review' },
  { value: WebhookTriggerEvent.MAIL_RELEASED.toString(), label: 'Mail Released by Reviewer' },
  { value: WebhookTriggerEvent.MAIL_QUARANTINE_REJECTED.toString(), label: 'Mail Rejected by Reviewer' },
  { value: WebhookTriggerEvent.MAIL_EXPIRED.toString(), label: 'Mail Expired Unreviewed' },
];

export const triggerEventLabels: Record<number, string> = {
  [WebhookTriggerEvent.UNSPECIFIED]: 'Unspecified',
  [WebhookTriggerEvent.MAIL_SENT]: 'Sent',
  [WebhookTriggerEvent.MAIL_DELIVERED]: 'Delivered',
  [WebhookTriggerEvent.MAIL_OPENED]: 'Opened',
  [WebhookTriggerEvent.MAIL_CLICKED]: 'Clicked',
  [WebhookTriggerEvent.MAIL_BOUNCED]: 'Bounced',
  [WebhookTriggerEvent.MAIL_REJECTED]: 'Rejected',
  [WebhookTriggerEvent.MAIL_INBOUND]: 'Inbound',
  [WebhookTriggerEvent.MAIL_HELD]: 'Held',
  [WebhookTriggerEvent.MAIL_RELEASED]: 'Released',
  [WebhookTriggerEvent.MAIL_QUARANTINE_REJECTED]: 'Rejected by Reviewer',
  [WebhookTriggerEvent.MAIL_EXPIRED]: 'Expired',
};
