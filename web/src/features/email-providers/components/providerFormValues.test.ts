import { describe, expect, test } from 'bun:test';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import { deriveSwitches, seedSmtpValues, toProviderRequest } from './providerFormValues';

const smtpForm = (smtp: Record<string, unknown>) => ({
  type: ProviderType.SMTP,
  smtp: {
    host: 'smtp.example.com',
    dkimEnabled: false,
    authMode: 'password' as const,
    dkim: { domain: '', selector: '', privateKey: '' },
    oauth2: { clientId: '', clientSecret: '', tokenEndpoint: '' },
    ...smtp,
  },
});

describe('toProviderRequest', () => {
  test('drops the switches, which are not fields the API knows about', () => {
    const out = toProviderRequest(smtpForm({}));
    expect(out.smtp).not.toHaveProperty('dkimEnabled');
    expect(out.smtp).not.toHaveProperty('authMode');
  });

  test('keeps DKIM values when the switch is on', () => {
    const dkim = { domain: 'example.com', selector: 's1', privateKey: 'KEY' };
    const out = toProviderRequest(smtpForm({ dkimEnabled: true, dkim }));
    expect(out.smtp?.dkim).toEqual(dkim);
  });

  // The one that matters: the server signs whenever the key is present, so
  // leaving it behind would keep signing mail from a provider whose DKIM
  // switch reads off.
  test('clears DKIM values when the switch is off', () => {
    const out = toProviderRequest(
      smtpForm({ dkimEnabled: false, dkim: { domain: 'example.com', selector: 's1', privateKey: 'KEY' } }),
    );
    expect(out.smtp?.dkim).toEqual({ domain: '', selector: '', privateKey: '' });
  });

  test('keeps OAuth values when that mode is selected', () => {
    const oauth2 = { clientId: 'id', clientSecret: 'secret', tokenEndpoint: 'https://t' };
    const out = toProviderRequest(smtpForm({ authMode: 'oauth2', oauth2 }));
    expect(out.smtp?.oauth2).toEqual(oauth2);
  });

  // Same failure mode as DKIM: OAuth credentials left in place would keep
  // XOAUTH2 in use for a provider switched back to password auth, and the
  // password would look like it was being ignored.
  test('clears OAuth values when switched back to password auth', () => {
    const out = toProviderRequest(
      smtpForm({ authMode: 'password', oauth2: { clientId: 'id', clientSecret: 'secret', tokenEndpoint: 'https://t' } }),
    );
    expect(out.smtp?.oauth2?.clientId).toBe('');
    expect(out.smtp?.oauth2?.clientSecret).toBe('');
    expect(out.smtp?.oauth2?.tokenEndpoint).toBe('');
  });

  test('turning DKIM off does not disturb the OAuth arm, or the reverse', () => {
    const out = toProviderRequest(
      smtpForm({
        dkimEnabled: false,
        authMode: 'oauth2',
        dkim: { domain: 'example.com', selector: 's1', privateKey: 'KEY' },
        oauth2: { clientId: 'id', clientSecret: 'secret', tokenEndpoint: 'https://t' },
      }),
    );
    expect(out.smtp?.dkim?.privateKey).toBe('');
    expect(out.smtp?.oauth2?.clientId).toBe('id');
  });

  test('leaves the other SMTP fields alone', () => {
    const out = toProviderRequest(smtpForm({ host: 'mail.example.com', port: 587 }));
    expect(out.smtp?.host).toBe('mail.example.com');
    expect(out.smtp?.port).toBe(587);
  });

  test('passes non-SMTP providers through untouched', () => {
    const values = { type: ProviderType.SENDGRID, sendgrid: { apiKey: 'SG.x' } };
    expect(toProviderRequest(values)).toEqual(values);
  });

  test('does not mutate the values it was given', () => {
    const values = smtpForm({ dkim: { domain: 'example.com', selector: 's', privateKey: 'K' } });
    toProviderRequest(values);
    expect(values.smtp.dkim.privateKey).toBe('K');
  });
});

describe('deriveSwitches', () => {
  // Reads redact secrets, so the switch has to be derived from a field that
  // survives redaction. Keying off privateKey would show DKIM as off for every
  // provider that has it configured.
  test('reads DKIM as on from the domain alone, with the key redacted', () => {
    expect(deriveSwitches({ dkim: { domain: 'example.com', selector: 's1', privateKey: '' } }).dkimEnabled).toBe(true);
  });

  test('reads OAuth as selected from the client id alone, with the secret redacted', () => {
    expect(deriveSwitches({ oauth2: { clientId: 'id', clientSecret: '' } }).authMode).toBe('oauth2');
  });

  test('an unconfigured provider gets both switches off', () => {
    expect(deriveSwitches({})).toEqual({ dkimEnabled: false, authMode: 'password' });
    expect(deriveSwitches(undefined)).toEqual({ dkimEnabled: false, authMode: 'password' });
  });

  test('round-trips: saving what was loaded preserves the configuration', () => {

    const saved = {
      host: 'smtp.example.com',
      dkim: { domain: 'example.com', selector: 's1', privateKey: '' },
      oauth2: { clientId: 'id', clientSecret: '', tokenEndpoint: 'https://t' },
    };
    const out = toProviderRequest({
      type: ProviderType.SMTP,
      smtp: { ...saved, ...deriveSwitches(saved) },
    });
    expect(out.smtp?.dkim?.domain).toBe('example.com');
    expect(out.smtp?.oauth2?.clientId).toBe('id');
  });
});

/**
 * The load-edit-save path, which is where the switches were being lost.
 *
 * A provider off the server has no dkimEnabled or authMode — they are form-only
 * fields. Rendering fell back sensibly, so the switch looked right, but the form
 * state still held undefined, and submit reads the state. Editing an unrelated
 * field and saving therefore wiped whichever feature the user had not touched.
 */
describe('editing a saved provider', () => {
  const DEFAULTS = {
    host: '', port: 587, username: '', password: '',
    dkimEnabled: false,
    authMode: 'password' as const,
    dkim: { domain: '', selector: '', privateKey: '' },
    oauth2: { mechanism: 'XOAUTH2', clientId: '', clientSecret: '', refreshToken: '', tokenEndpoint: '', scope: '' },
  };

  // As the API returns it: secrets redacted, no form-only fields.
  const fromServer = {
    host: 'smtp.example.com',
    port: 587,
    username: 'postmaster',
    password: '',
    dkim: { domain: 'example.com', selector: 's1', privateKey: '' },
  };

  test('renaming a DKIM-signed provider does not drop its signing config', () => {
    const seeded = seedSmtpValues(fromServer, DEFAULTS);
    const out = toProviderRequest({ type: ProviderType.SMTP, name: 'Renamed', smtp: seeded });

    expect(out.smtp?.dkim?.domain).toBe('example.com');
    expect(out.smtp?.dkim?.selector).toBe('s1');
  });

  test('renaming an OAuth provider does not drop its credentials', () => {
    const oauthProvider = {
      host: 'smtp.office365.com',
      oauth2: { mechanism: 'XOAUTH2', clientId: 'client-id', clientSecret: '', refreshToken: '', tokenEndpoint: 'https://login.example/token', scope: '' },
    };
    const seeded = seedSmtpValues(oauthProvider, DEFAULTS);
    expect(seeded.authMode).toBe('oauth2');

    const out = toProviderRequest({ type: ProviderType.SMTP, smtp: seeded });
    expect(out.smtp?.oauth2?.clientId).toBe('client-id');
    expect(out.smtp?.oauth2?.tokenEndpoint).toBe('https://login.example/token');
  });

  test('a plain provider still saves with both features off', () => {
    const seeded = seedSmtpValues({ host: 'smtp.example.com', username: 'u' }, DEFAULTS);
    const out = toProviderRequest({ type: ProviderType.SMTP, smtp: seeded });

    expect(out.smtp?.dkim).toEqual({ domain: '', selector: '', privateKey: '' });
    expect(out.smtp?.oauth2?.clientId).toBe('');
  });

  test('turning DKIM off on a saved provider still clears it', () => {
    const seeded = seedSmtpValues(fromServer, DEFAULTS);
    const out = toProviderRequest({
      type: ProviderType.SMTP,
      smtp: { ...seeded, dkimEnabled: false },
    });
    expect(out.smtp?.dkim?.domain).toBe('');
  });

  test('fields the server did not return fall back to the defaults', () => {
    const seeded = seedSmtpValues({ host: 'smtp.example.com' }, DEFAULTS);
    expect(seeded.port).toBe(587);
    expect(seeded.oauth2).toEqual(DEFAULTS.oauth2);
  });

  test('a new provider seeds cleanly from the defaults alone', () => {
    expect(seedSmtpValues(undefined, DEFAULTS)).toEqual({
      ...DEFAULTS,
      dkimEnabled: false,
      authMode: 'password',
    });
  });
});
