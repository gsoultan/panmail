import { describe, expect, test } from 'bun:test';
import {
  RETENTION_FIELDS,
  describeRetention,
  destructiveChanges,
  retentionValues,
  type RetentionKey,
} from './retentionFields';

/**
 * What gets confirmed before it is saved.
 *
 * Saving triggers a retention pass immediately, so this is the difference
 * between a setting and a deletion. Over-confirming trains people to click
 * through the dialog, so only changes that destroy the only copy panmail holds
 * are in scope — and only when they actually shorten the policy.
 */
describe('destructiveChanges', () => {
  const cases: {
    name: string;
    before: Partial<Record<RetentionKey, number>>;
    after: Partial<Record<RetentionKey, number>>;
    want: RetentionKey[];
  }[] = [
    {
      name: 'turning a policy on deletes everything past the new cutoff',
      before: { messageRetentionDays: 0 },
      after: { messageRetentionDays: 30 },
      want: ['messageRetentionDays'],
    },
    {
      name: 'shortening an existing policy deletes the difference',
      before: { inboundRetentionDays: 90 },
      after: { inboundRetentionDays: 30 },
      want: ['inboundRetentionDays'],
    },
    {
      name: 'lengthening a policy risks nothing that already exists',
      before: { messageRetentionDays: 30 },
      after: { messageRetentionDays: 90 },
      want: [],
    },
    {
      name: 'turning a policy off is not a shorter retention, it is forever',
      before: { archiveRetentionDays: 30 },
      after: { archiveRetentionDays: 0 },
      want: [],
    },
    {
      name: 'no change at all',
      before: { messageRetentionDays: 30, inboundRetentionDays: 30 },
      after: { messageRetentionDays: 30, inboundRetentionDays: 30 },
      want: [],
    },
    {
      // Delivery events are archived on their way out and webhook logs are
      // operational noise. Confirming those would bury the ones that matter.
      name: 'classes that are archived or replaceable are not confirmed',
      before: { logRetentionDays: 90, webhookRetentionDays: 30, appLogRetentionDays: 30 },
      after: { logRetentionDays: 1, webhookRetentionDays: 1, appLogRetentionDays: 1 },
      want: [],
    },
    {
      name: 'several destructive changes at once are all reported',
      before: { messageRetentionDays: 0, inboundRetentionDays: 90, archiveRetentionDays: 0 },
      after: { messageRetentionDays: 7, inboundRetentionDays: 30, archiveRetentionDays: 365 },
      want: ['messageRetentionDays', 'archiveRetentionDays', 'inboundRetentionDays'],
    },
  ];

  for (const tc of cases) {
    test(tc.name, () => {
      const got = destructiveChanges(tc.before, tc.after).map((change) => change.field.key);
      expect(got.sort()).toEqual([...tc.want].sort());
    });
  }

  test('reports what the change was, so the dialog can say it', () => {
    const [change] = destructiveChanges({ inboundRetentionDays: 90 }, { inboundRetentionDays: 30 });
    expect(change.from).toBe(90);
    expect(change.to).toBe(30);
    expect(change.field.label).toBe('Received mail');
  });
});

describe('describeRetention', () => {
  const field = RETENTION_FIELDS.find((f) => f.key === 'messageRetentionDays')!;

  test('zero reads as the field’s own idea of forever', () => {
    expect(describeRetention(0, field)).toBe(field.foreverHint);
  });

  test('one day is not plural', () => {
    expect(describeRetention(1, field)).toBe('Deleted after 1 day.');
  });

  test('more than one day is', () => {
    expect(describeRetention(30, field)).toBe('Deleted after 30 days.');
  });
});

describe('retentionValues', () => {
  test('absent settings read as keep-forever rather than NaN', () => {
    const values = retentionValues(undefined);
    for (const field of RETENTION_FIELDS) {
      expect(values[field.key]).toBe(0);
    }
  });

  test('reads every field the backend sends', () => {
    const values = retentionValues({
      logRetentionDays: 14,
      messageRetentionDays: 7,
      outboxRetentionDays: 21,
      webhookRetentionDays: 3,
      appLogRetentionDays: 5,
      inboundRetentionDays: 90,
      archiveRetentionDays: 365,
      quarantineRetentionDays: 30,
    } as never);

    expect(values).toEqual({
      logRetentionDays: 14,
      messageRetentionDays: 7,
      outboxRetentionDays: 21,
      webhookRetentionDays: 3,
      appLogRetentionDays: 5,
      inboundRetentionDays: 90,
      archiveRetentionDays: 365,
      quarantineRetentionDays: 30,
    });
  });

  test('every field the backend can prune has a knob', () => {
    // A store that gains retention on the backend and not here is a store that
    // grows where nobody can see it.
    const expected: RetentionKey[] = [
      'appLogRetentionDays',
      'archiveRetentionDays',
      'inboundRetentionDays',
      'logRetentionDays',
      'messageRetentionDays',
      'outboxRetentionDays',
      'quarantineRetentionDays',
      'webhookRetentionDays',
    ];
    expect(RETENTION_FIELDS.map((f) => f.key).sort()).toEqual(expected.sort());
  });
});
