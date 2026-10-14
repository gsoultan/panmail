import { describe, expect, test } from 'bun:test';
import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import { UpdateEmailProviderRequestSchema } from '../../../api/panmail/v1/email_provider_service_pb';
import { SmtpConfigSchema } from '../../../api/panmail/v1/email_provider_pb';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import { seedSmtpValues, toProviderRequest } from '../components/providerFormValues';
import { toInit } from './emailProvider';

const SMTP_DEFAULTS = {
  host: '', port: 587, username: '', password: '',
  dkimEnabled: false,
  dkim: { domain: '', selector: '', privateKey: '' },
  authMode: 'password',
  oauth2: { mechanism: 'XOAUTH2', clientId: '', clientSecret: '', refreshToken: '', tokenEndpoint: '', scope: '' },
};

// What the edit form submits for a provider loaded off the server: the config
// branch started life as a message, so it still carries $typeName.
const submittedSmtp = (loaded: ReturnType<typeof create<typeof SmtpConfigSchema>>) =>
  toProviderRequest({ type: ProviderType.SMTP, smtp: seedSmtpValues(loaded as any, SMTP_DEFAULTS) }).smtp;

const roundTrip = (smtp: unknown) => {
  const req = create(UpdateEmailProviderRequestSchema, {
    id: 'p1',
    config: { case: 'smtp', value: toInit(smtp) },
  });
  return fromBinary(UpdateEmailProviderRequestSchema, toBinary(UpdateEmailProviderRequestSchema, req));
};

describe('toInit', () => {
  test('a loaded SMTP provider without DKIM serializes after the form clears its DKIM block', () => {
    const loaded = create(SmtpConfigSchema, { host: 'smtp.example.com', port: 587, username: 'u' });

    const sent = roundTrip(submittedSmtp(loaded));

    expect(sent.config.case).toBe('smtp');
    expect(sent.config.value).toMatchObject({ host: 'smtp.example.com', port: 587, username: 'u' });
  });

  test('a loaded SMTP provider keeps its DKIM values on save', () => {
    const loaded = create(SmtpConfigSchema, {
      host: 'smtp.example.com',
      dkim: { domain: 'example.com', selector: 's1', privateKey: 'key' },
    });

    const sent = roundTrip(submittedSmtp(loaded));

    expect(sent.config.case === 'smtp' && sent.config.value.dkim).toMatchObject({
      domain: 'example.com', selector: 's1', privateKey: 'key',
    });
  });

  test('leaves bytes and scalars alone', () => {
    const bytes = new Uint8Array([1, 2]);
    expect(toInit({ $typeName: 'x', a: bytes, b: [{ $typeName: 'y', c: 1 }] })).toEqual({ a: bytes, b: [{ c: 1 }] });
  });
});
