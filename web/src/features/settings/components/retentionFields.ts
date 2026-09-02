import type { SystemSettings } from '../../../api/panmail/v1/system_settings_pb';

/**
 * The retention knobs, as one list.
 *
 * Every class of data panmail stores appears here. A class that is missing is
 * a store that grows without bound and that nobody can see, which is the
 * condition this page exists to make visible — so adding a store to the
 * backend means adding it here too.
 */
export type RetentionKey =
  | 'logRetentionDays'
  | 'messageRetentionDays'
  | 'archiveRetentionDays'
  | 'outboxRetentionDays'
  | 'webhookRetentionDays'
  | 'appLogRetentionDays'
  | 'inboundRetentionDays'
  | 'quarantineRetentionDays';

export type RetentionGroup = 'delivery' | 'queues' | 'system';

export interface RetentionField {
  key: RetentionKey;
  group: RetentionGroup;
  label: string;
  description: string;
  /**
   * Whether shortening this retention destroys the only copy panmail holds.
   * Those changes are confirmed before they are saved; the rest are not,
   * because a shorter webhook log is not something anyone needs protecting
   * from.
   */
  destructive: boolean;
  /** What "0" means for this field, said in the field's own terms. */
  foreverHint: string;
}

export const RETENTION_GROUPS: Record<RetentionGroup, { title: string; blurb: string }> = {
  delivery: {
    title: 'Delivery history',
    blurb: 'What the analytics and archive pages read from.',
  },
  queues: {
    title: 'Queues',
    blurb: 'Work that has finished, kept only so failures stay diagnosable.',
  },
  system: {
    title: 'System',
    blurb: 'Operational data that is not part of a delivery record.',
  },
};

export const RETENTION_FIELDS: RetentionField[] = [
  {
    key: 'logRetentionDays',
    group: 'delivery',
    label: 'Delivery events',
    description:
      'Opens, clicks, bounces and deliveries. Expired events are written to a JSONL archive before they leave the database, so this shortens what analytics can query rather than destroying the record.',
    destructive: false,
    foreverHint: 'Keep every event queryable forever.',
  },
  {
    key: 'messageRetentionDays',
    group: 'delivery',
    label: 'Message content',
    description:
      'Subjects, rendered bodies and attachments as sent. Deleted outright and never archived — an archive of the content would be the content. Delivery events survive; only what was in the message goes.',
    destructive: true,
    foreverHint: 'Keep every body and attachment forever.',
  },
  {
    key: 'archiveRetentionDays',
    group: 'delivery',
    label: 'Event archives',
    description:
      'The JSONL files written when delivery events expire, listed on the Archives page. This is the escape hatch for the events above, so deleting them ends the delivery record for good.',
    destructive: true,
    foreverHint: 'Keep every archive file forever.',
  },
  {
    key: 'outboxRetentionDays',
    group: 'queues',
    label: 'Failed sends',
    description:
      'Outbox rows for mail that permanently failed. Delivered mail is removed as it goes; only failures accumulate, and each one carries the whole request including the body.',
    destructive: false,
    foreverHint: 'Keep every failed send forever.',
  },
  {
    key: 'webhookRetentionDays',
    group: 'queues',
    label: 'Webhook notifications',
    description: 'Notifications that have been delivered or have permanently failed.',
    destructive: false,
    foreverHint: 'Keep every finished notification forever.',
  },
  {
    key: 'appLogRetentionDays',
    group: 'system',
    label: 'Application logs',
    description: 'The gateway’s own log entries, shown on the Logs page.',
    destructive: false,
    foreverHint: 'Keep every log entry forever.',
  },
  {
    key: 'inboundRetentionDays',
    group: 'system',
    label: 'Received mail',
    description:
      'Inbound messages and their bodies. This is the only copy panmail holds of mail somebody sent you; nothing archives it first.',
    destructive: true,
    foreverHint: 'Keep received mail forever.',
  },
  {
    key: 'quarantineRetentionDays',
    group: 'system',
    label: 'Held for review',
    description:
      'Messages a filter rule stopped, waiting in the review queue. A message that expires here was never decided by anyone — it is simply not sent, and not received. Applies to messages held from now on; anything already waiting keeps the deadline it was given.',
    destructive: true,
    foreverHint: 'Hold messages until somebody reviews them.',
  },
];

export const RETENTION_MIN_DAYS = 0;
export const RETENTION_MAX_DAYS = 3650;

export interface RetentionChange {
  field: RetentionField;
  from: number;
  to: number;
}

/**
 * The changes that will delete data the moment they are saved.
 *
 * A retention only ever deletes when it gets *shorter*, and zero is not a
 * shorter retention — it is "keep forever", so turning a policy off is never
 * flagged. Lengthening one is not flagged either: nothing that already exists
 * is at risk from being kept longer.
 */
export const destructiveChanges = (
  before: Partial<Record<RetentionKey, number>>,
  after: Partial<Record<RetentionKey, number>>,
): RetentionChange[] =>
  RETENTION_FIELDS.filter((field) => field.destructive).flatMap((field) => {
    const from = before[field.key] ?? 0;
    const to = after[field.key] ?? 0;

    // Enabling a policy that was off deletes everything past the new cutoff,
    // which is the single most destructive thing this page can do.
    const enabled = from === 0 && to > 0;
    const shortened = from > 0 && to > 0 && to < from;

    return enabled || shortened ? [{ field, from, to }] : [];
  });

/** How a stored value reads back to a person. */
export const describeRetention = (days: number, field: RetentionField): string =>
  days <= 0 ? field.foreverHint : `Deleted after ${days} ${days === 1 ? 'day' : 'days'}.`;

/** The retention values out of a settings message, with defaults for absent ones. */
export const retentionValues = (
  settings: Partial<SystemSettings> | undefined,
): Record<RetentionKey, number> =>
  RETENTION_FIELDS.reduce(
    (values, field) => {
      const raw = settings?.[field.key];
      values[field.key] = typeof raw === 'number' && Number.isFinite(raw) ? raw : 0;
      return values;
    },
    {} as Record<RetentionKey, number>,
  );
