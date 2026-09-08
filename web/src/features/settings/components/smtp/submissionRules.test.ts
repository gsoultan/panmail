import { describe, expect, test } from 'bun:test';
import {
  initialFormValues,
  problemFor,
  validateForm,
  willHaveTls,
  type SmtpSubmissionFormValues,
} from './submissionRules';
import { SmtpBindScope, type SmtpSubmission } from '../../../../api/panmail/v1/system_settings_pb';

const values = (fields: Partial<SmtpSubmissionFormValues> = {}): SmtpSubmissionFormValues => ({
  enabled: true,
  bindScope: SmtpBindScope.LOOPBACK,
  port: 587,
  certificatePem: '',
  privateKeyPem: '',
  allowInsecureAuth: true,
  clearTls: false,
  ...fields,
});

const PEM_CERT = '-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----';
const PEM_KEY = '-----BEGIN PRIVATE KEY-----\nx\n-----END PRIVATE KEY-----';

/**
 * The browser-side copy of the server's rules.
 *
 * These exist to explain a refusal at the point of the mistake. They are not
 * the enforcement — internal/smtp_submission/entities is, and has to be, since
 * this file runs where the caller can edit it. What these tests protect is that
 * the two agree, so the panel never encourages a save the gateway will reject.
 */
describe('validateForm', () => {
  test('a loopback listener with sign-in over plain text is allowed', () => {
    expect(validateForm(values(), false)).toEqual([]);
  });

  test('sign-in without TLS is refused once other machines can reach it', () => {
    const problems = validateForm(
      values({ bindScope: SmtpBindScope.ALL_INTERFACES, allowInsecureAuth: true }),
      false,
    );
    expect(problemFor(problems, 'allowInsecureAuth')).toContain('only possible on a listener this machine alone');
  });

  test('a listener on every interface needs a certificate', () => {
    const problems = validateForm(
      values({ bindScope: SmtpBindScope.ALL_INTERFACES, allowInsecureAuth: false }),
      false,
    );
    expect(problemFor(problems, 'certificatePem')).toContain('needs a TLS certificate');
  });

  test('a stored certificate satisfies the requirement without re-pasting it', () => {
    expect(
      validateForm(values({ bindScope: SmtpBindScope.ALL_INTERFACES, allowInsecureAuth: false }), true),
    ).toEqual([]);
  });

  test('a freshly pasted pair satisfies it too', () => {
    const problems = validateForm(
      values({
        bindScope: SmtpBindScope.ALL_INTERFACES,
        allowInsecureAuth: false,
        certificatePem: PEM_CERT,
        privateKeyPem: PEM_KEY,
      }),
      false,
    );
    expect(problems).toEqual([]);
  });

  test('a certificate without its key is refused', () => {
    const problems = validateForm(values({ certificatePem: PEM_CERT }), false);
    expect(problemFor(problems, 'privateKeyPem')).toContain('together');
  });

  test('removing and installing at once is refused', () => {
    const problems = validateForm(
      values({ clearTls: true, certificatePem: PEM_CERT, privateKeyPem: PEM_KEY }),
      true,
    );
    expect(problemFor(problems, 'form')).toContain('opposite instructions');
  });

  test('clearing the stored pair leaves nothing to authenticate with', () => {
    const problems = validateForm(
      values({ clearTls: true, allowInsecureAuth: false, bindScope: SmtpBindScope.LOOPBACK }),
      true,
    );
    expect(problemFor(problems, 'form')).toContain('Otherwise nothing can authenticate');
  });

  test('port 25 is refused with the reason', () => {
    const problems = validateForm(values({ port: 25 }), false);
    expect(problemFor(problems, 'port')).toContain('mail exchanger port');
  });

  test.each([0, -1, 70000, 1.5])('port %p is out of range', (port) => {
    expect(problemFor(validateForm(values({ port }), false), 'port')).toContain('between 1 and 65535');
  });

  test('a disabled listener with nothing configured is savable', () => {
    // Turning it off must never be blocked by the rules that govern turning it
    // on, or an administrator could not undo a configuration they regret.
    expect(validateForm(values({ enabled: false, allowInsecureAuth: false }), false)).toEqual([]);
  });
});

describe('willHaveTls', () => {
  const cases: { name: string; values: Partial<SmtpSubmissionFormValues>; stored: boolean; want: boolean }[] = [
    { name: 'nothing stored and nothing pasted', values: {}, stored: false, want: false },
    { name: 'stored and untouched', values: {}, stored: true, want: true },
    { name: 'stored but being removed', values: { clearTls: true }, stored: true, want: false },
    {
      name: 'a new pair replaces what is stored',
      values: { certificatePem: PEM_CERT, privateKeyPem: PEM_KEY },
      stored: false,
      want: true,
    },
    { name: 'whitespace is not a certificate', values: { certificatePem: '   ' }, stored: false, want: false },
  ];

  for (const c of cases) {
    test(c.name, () => {
      expect(willHaveTls(values(c.values), c.stored)).toBe(c.want);
    });
  }
});

describe('initialFormValues', () => {
  test('an absent listener defaults to loopback on the submission port', () => {
    const initial = initialFormValues(null);
    expect(initial.enabled).toBe(false);
    expect(initial.bindScope).toBe(SmtpBindScope.LOOPBACK);
    expect(initial.port).toBe(587);
  });

  test('the PEM fields always start empty, because the server never sends a key back', () => {
    const initial = initialFormValues({
      enabled: true,
      bindScope: SmtpBindScope.ALL_INTERFACES,
      port: 2525,
      starttls: true,
    } as SmtpSubmission);
    expect(initial.certificatePem).toBe('');
    expect(initial.privateKeyPem).toBe('');
    expect(initial.clearTls).toBe(false);
    expect(initial.port).toBe(2525);
  });
});
