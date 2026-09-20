import { describe, expect, test } from 'bun:test';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import {
  canShowWebhookUrl,
  inboundWebhookUrl,
  secretFieldFor,
  webhookTypeFor,
} from './inboundWebhook';

describe('webhookTypeFor', () => {
  // The segment is the contract with the Go handler's switch, not a label.
  // A rename here routes every event to the generic verifier, which refuses
  // them all with a 401 that explains nothing.
  test.each([
    [ProviderType.SENDGRID, 'sendgrid'],
    [ProviderType.MAILGUN, 'mailgun'],
    [ProviderType.POSTMARK, 'postmark'],
    [ProviderType.SES, 'ses'],
  ])('%s routes to its own handler', (type, expected) => {
    expect(webhookTypeFor(type)).toBe(expected);
  });

  // An SMTP send learns its outcome from the conversation; there is no third
  // party to post anything back, so offering a URL would invite an operator to
  // configure something that can never fire.
  test.each([ProviderType.SMTP, ProviderType.IMAP, ProviderType.POP3])(
    'transport type %s has no inbound events',
    (type) => {
      expect(webhookTypeFor(type)).toBeNull();
    },
  );
});

describe('inboundWebhookUrl', () => {
  const parts = {
    baseUrl: 'https://mail.example.com',
    tenantId: 'tenant-1',
    providerId: 'prov-1',
    type: 'sendgrid',
  };

  test('builds the path the handler parses', () => {
    expect(inboundWebhookUrl(parts)).toBe(
      'https://mail.example.com/webhooks/tenant-1/prov-1/sendgrid',
    );
  });

  test('a trailing slash on the base does not double up', () => {
    expect(inboundWebhookUrl({ ...parts, baseUrl: 'https://mail.example.com/' })).toBe(
      'https://mail.example.com/webhooks/tenant-1/prov-1/sendgrid',
    );
    expect(inboundWebhookUrl({ ...parts, baseUrl: 'https://mail.example.com///' })).toBe(
      'https://mail.example.com/webhooks/tenant-1/prov-1/sendgrid',
    );
  });

  // Half a URL pasted into a provider's dashboard fails silently, so an
  // incomplete one is not rendered at all.
  test.each([
    ['no base url', { baseUrl: '' }],
    ['no tenant', { tenantId: '' }],
    ['no provider', { providerId: '' }],
    ['no type', { type: '' }],
  ])('%s yields nothing rather than a partial url', (_name, override) => {
    expect(inboundWebhookUrl({ ...parts, ...override })).toBe('');
  });
});

describe('secretFieldFor', () => {
  // One column, four different things on the wire. The field has to say which
  // one it wants, or an operator pastes a SendGrid key into an SES provider.
  test('every provider that posts events describes its own secret', () => {
    const types = [
      ProviderType.SENDGRID,
      ProviderType.MAILGUN,
      ProviderType.POSTMARK,
      ProviderType.SES,
    ];

    const labels = new Set<string>();
    for (const type of types) {
      const field = secretFieldFor(type);
      expect(field).not.toBeNull();
      expect(field!.label).not.toBe('');
      expect(field!.description).not.toBe('');
      labels.add(field!.label);
    }
    expect(labels.size).toBe(types.length);
  });

  test('postmark asks for basic credentials, not a signing key', () => {
    expect(secretFieldFor(ProviderType.POSTMARK)!.placeholder).toBe('user:password');
  });

  test('ses asks for the topic arn', () => {
    const field = secretFieldFor(ProviderType.SES)!;
    expect(field.placeholder).toStartWith('arn:aws:sns:');
    expect(field.description).toContain('topic');
  });

  test('a transport provider has no secret to ask for', () => {
    expect(secretFieldFor(ProviderType.SMTP)).toBeNull();
  });
});

describe('canShowWebhookUrl', () => {
  // The URL contains the provider's id, which does not exist until it is saved.
  test('not until the provider has an id', () => {
    expect(canShowWebhookUrl(undefined)).toBe(false);
    expect(canShowWebhookUrl('')).toBe(false);
    expect(canShowWebhookUrl('prov-1')).toBe(true);
  });
});

// The type/secret pairing is the thing that breaks silently: a provider that
// routes to its own handler but has no secret guidance would render a field
// nobody can fill correctly, and vice versa.
describe('the routed types and the described types are the same set', () => {
  const all = [
    ProviderType.SMTP,
    ProviderType.IMAP,
    ProviderType.POP3,
    ProviderType.SENDGRID,
    ProviderType.SES,
    ProviderType.POSTMARK,
    ProviderType.MAILGUN,
  ];

  test.each(all)('provider type %s agrees with itself', (type) => {
    expect(webhookTypeFor(type) === null).toBe(secretFieldFor(type) === null);
  });
});
