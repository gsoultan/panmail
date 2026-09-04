import { describe, expect, test } from 'bun:test';
import { create, toBinary, fromBinary } from '@bufbuild/protobuf';
import { SystemSettingsSchema } from '../../../api/panmail/v1/system_settings_pb';

// The dashboard posts the whole form, and "keep forever" is a zero. Presence
// tracking is what stops that zero from being read as "I did not mention it" —
// so an explicit 0 has to survive the wire as *set*, not be dropped as a
// default. If this ever breaks, an operator choosing forever silently gets
// "leave alone" instead.
describe('settings field presence', () => {
  test('an explicit zero round-trips as present', () => {
    const msg = create(SystemSettingsSchema, { logRetentionDays: 0 });
    const back = fromBinary(SystemSettingsSchema, toBinary(SystemSettingsSchema, msg));
    expect(back.logRetentionDays).toBe(0);
    expect(back.logRetentionDays).not.toBeUndefined();
  });

  test('an omitted field stays undefined', () => {
    const msg = create(SystemSettingsSchema, { baseUrl: 'https://mail.example.com' });
    const back = fromBinary(SystemSettingsSchema, toBinary(SystemSettingsSchema, msg));
    expect(back.baseUrl).toBe('https://mail.example.com');
    // Not mentioned, so the gateway must leave it alone rather than zero it.
    expect(back.logRetentionDays).toBeUndefined();
    expect(back.messageRetentionDays).toBeUndefined();
  });

  test('a full form sends every retention field', () => {
    const msg = create(SystemSettingsSchema, {
      baseUrl: 'https://mail.example.com',
      logRetentionDays: 14,
      messageRetentionDays: 0,
      outboxRetentionDays: 0,
      webhookRetentionDays: 7,
      appLogRetentionDays: 0,
      inboundRetentionDays: 0,
      archiveRetentionDays: 0,
      quarantineRetentionDays: 0,
    });
    const back = fromBinary(SystemSettingsSchema, toBinary(SystemSettingsSchema, msg));
    for (const k of ['logRetentionDays', 'messageRetentionDays', 'outboxRetentionDays',
      'webhookRetentionDays', 'appLogRetentionDays', 'inboundRetentionDays',
      'archiveRetentionDays', 'quarantineRetentionDays'] as const) {
      expect(back[k], `${k} was dropped from the payload`).not.toBeUndefined();
    }
  });
});
