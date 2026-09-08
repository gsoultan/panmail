import { describe, expect, test } from 'bun:test';
import { describeConnection, hostFromBaseUrl } from './smtpConnection';
import type { SmtpSubmission } from '../../../../api/panmail/v1/system_settings_pb';

// Build a reported listener state. The generated type carries a $typeName, so
// the cast keeps the fixtures readable without asserting a whole message.
const submission = (fields: Partial<SmtpSubmission>): SmtpSubmission =>
  ({ enabled: true, host: '', port: 587, starttls: true, insecureAuthAllowed: false, ...fields }) as SmtpSubmission;

/**
 * What the panel tells someone to connect to.
 *
 * A wildcard bind names no reachable host, so the server reports none rather
 * than claiming 0.0.0.0 answers. Falling back to the base URL is a guess, and
 * the panel labels it as one — pasting a wrong hostname into a config file is
 * the kind of mistake that costs an afternoon.
 */
describe('describeConnection', () => {
  const cases: {
    name: string;
    submission: SmtpSubmission | null | undefined;
    baseUrl: string | undefined;
    wantEnabled: boolean;
    wantHost: string;
    wantSource: string;
    wantEncryption: string;
  }[] = [
    {
      name: 'a listener with its own hostname is reported as authoritative',
      submission: submission({ host: 'mail.example.com' }),
      baseUrl: 'https://panmail.example.com',
      wantEnabled: true,
      wantHost: 'mail.example.com',
      wantSource: 'listener',
      wantEncryption: 'STARTTLS',
    },
    {
      name: 'a wildcard bind falls back to the base URL host',
      submission: submission({ host: '' }),
      baseUrl: 'https://panmail.example.com',
      wantEnabled: true,
      wantHost: 'panmail.example.com',
      wantSource: 'baseUrl',
      wantEncryption: 'STARTTLS',
    },
    {
      name: 'a wildcard bind with no base URL admits it knows no host',
      submission: submission({ host: '' }),
      baseUrl: '',
      wantEnabled: true,
      wantHost: '',
      wantSource: 'unknown',
      wantEncryption: 'STARTTLS',
    },
    {
      name: 'a malformed base URL is not passed off as a hostname',
      submission: submission({ host: '' }),
      baseUrl: 'not a url',
      wantEnabled: true,
      wantHost: '',
      wantSource: 'unknown',
      wantEncryption: 'STARTTLS',
    },
    {
      name: 'no STARTTLS is reported as no encryption',
      submission: submission({ host: 'mail.example.com', starttls: false }),
      baseUrl: 'https://panmail.example.com',
      wantEnabled: true,
      wantHost: 'mail.example.com',
      wantSource: 'listener',
      wantEncryption: 'None',
    },
    {
      name: 'a disabled listener reports nothing to connect to',
      submission: submission({ enabled: false, host: 'mail.example.com' }),
      baseUrl: 'https://panmail.example.com',
      wantEnabled: false,
      wantHost: '',
      wantSource: 'unknown',
      wantEncryption: 'None',
    },
    {
      name: 'a null listener block is treated as disabled',
      submission: null,
      baseUrl: 'https://panmail.example.com',
      wantEnabled: false,
      wantHost: '',
      wantSource: 'unknown',
      wantEncryption: 'None',
    },
    {
      name: 'a response with no listener block at all is treated as disabled',
      submission: undefined,
      baseUrl: 'https://panmail.example.com',
      wantEnabled: false,
      wantHost: '',
      wantSource: 'unknown',
      wantEncryption: 'None',
    },
  ];

  for (const c of cases) {
    test(c.name, () => {
      const got = describeConnection(c.submission, c.baseUrl);
      expect(got.enabled).toBe(c.wantEnabled);
      expect(got.host).toBe(c.wantHost);
      expect(got.hostSource).toBe(c.wantSource as never);
      expect(got.encryption).toBe(c.wantEncryption as never);
    });
  }

  // The warning this drives is the difference between an API key crossing a
  // private hop and crossing the internet in the clear.
  test('insecure auth is surfaced, not swallowed', () => {
    const got = describeConnection(
      submission({ host: 'mail.example.com', starttls: false, insecureAuthAllowed: true }),
      'https://panmail.example.com',
    );
    expect(got.insecureAuthAllowed).toBe(true);
  });

  test('a disabled listener never reports insecure auth', () => {
    const got = describeConnection(
      submission({ enabled: false, insecureAuthAllowed: true }),
      'https://panmail.example.com',
    );
    expect(got.insecureAuthAllowed).toBe(false);
  });
});

describe('hostFromBaseUrl', () => {
  const cases: { name: string; input: string | undefined; want: string }[] = [
    { name: 'https URL', input: 'https://mail.example.com', want: 'mail.example.com' },
    { name: 'URL with a port', input: 'https://mail.example.com:8443', want: 'mail.example.com' },
    { name: 'URL with a path', input: 'https://mail.example.com/panmail', want: 'mail.example.com' },
    { name: 'empty', input: '', want: '' },
    { name: 'undefined', input: undefined, want: '' },
    { name: 'not a URL', input: 'mail.example.com', want: '' },
  ];

  for (const c of cases) {
    test(c.name, () => {
      expect(hostFromBaseUrl(c.input)).toBe(c.want);
    });
  }
});
