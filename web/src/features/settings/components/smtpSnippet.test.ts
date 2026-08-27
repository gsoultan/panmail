import { describe, expect, test } from 'bun:test';
import { buildSmtpSnippet, type SmtpSnippetValues } from './smtpSnippet';
import type { SmtpConnection } from './smtpConnection';

const connection = (over: Partial<SmtpConnection> = {}): SmtpConnection => ({
  enabled: true,
  host: 'mail.example.com',
  hostSource: 'listener',
  port: 587,
  encryption: 'STARTTLS',
  insecureAuthAllowed: false,
  ...over,
});

const values = (over: Partial<SmtpSnippetValues> = {}): SmtpSnippetValues => ({
  providerId: '3f1c2b7a-0000-4000-8000-000000000001',
  from: 'app@example.com',
  to: ['user@example.net'],
  cc: [],
  bcc: [],
  subject: 'Hello',
  bodyHtml: '',
  bodyText: 'hi there',
  ...over,
});

/**
 * The snippet is the instruction people follow, so it has to agree with what
 * the gateway actually does. The Bcc rule is the one that matters: over SMTP a
 * blind recipient belongs in the envelope and nowhere else, and a snippet that
 * put one in a header would teach precisely the leak the transport prevents.
 */
describe('buildSmtpSnippet', () => {
  test('a blind recipient reaches the envelope but never the message', () => {
    const got = buildSmtpSnippet(
      values({ to: ['visible@example.net'], cc: ['copied@example.net'], bcc: ['blind@example.net'] }),
      connection(),
    );

    // Every address is addressed with RCPT TO, which swaks spells --to.
    expect(got.swaks).toContain('visible@example.net,copied@example.net,blind@example.net');

    // But only the visible ones become headers.
    expect(got.message).toContain('To: visible@example.net');
    expect(got.message).toContain('Cc: copied@example.net');
    expect(got.message).not.toContain('blind@example.net');
    expect(got.swaks).not.toContain("'To: visible@example.net, blind@example.net'");
  });

  test('a blind recipient is called out in the notes', () => {
    const got = buildSmtpSnippet(values({ bcc: ['blind@example.net'] }), connection());
    expect(got.notes.some((n) => n.includes('envelope'))).toBe(true);
  });

  test('no Bcc means no note about it', () => {
    const got = buildSmtpSnippet(values(), connection());
    expect(got.notes.some((n) => n.includes('envelope'))).toBe(false);
  });

  test('duplicate addresses across the lists are sent once', () => {
    const got = buildSmtpSnippet(
      values({ to: ['user@example.net'], cc: ['User@Example.net'], bcc: ['other@example.net'] }),
      connection(),
    );
    expect(got.swaks).toContain("--to 'user@example.net,other@example.net'");
  });

  test('the live host and port are used, not a guess', () => {
    const got = buildSmtpSnippet(values(), connection({ host: 'smtp.internal', port: 2525 }));
    expect(got.swaks).toContain('swaks --server smtp.internal:2525');
  });

  test('a disabled listener falls back to a placeholder host', () => {
    const got = buildSmtpSnippet(
      values(),
      connection({ enabled: false, host: '', port: 0, encryption: 'None' }),
    );
    expect(got.swaks).toContain('YOUR_PANMAIL_HOST:587');
  });

  test('STARTTLS adds --tls, and its absence is flagged', () => {
    expect(buildSmtpSnippet(values(), connection()).swaks).toContain('--tls');

    const plain = buildSmtpSnippet(values(), connection({ encryption: 'None' }));
    expect(plain.swaks).not.toContain('--tls');
    expect(plain.notes.some((n) => n.includes('in the clear'))).toBe(true);
  });

  // An apostrophe in a subject would otherwise close the shell quoting and
  // produce a command that fails for reasons nobody would trace back here.
  test('an apostrophe in the subject does not break the shell quoting', () => {
    const got = buildSmtpSnippet(values({ subject: "Bob's receipt" }), connection());
    expect(got.swaks).toContain(`'Subject: Bob'\\''s receipt'`);
  });

  test('an html body sets the content type in both forms', () => {
    const got = buildSmtpSnippet(
      values({ bodyHtml: '<h1>hi</h1>', bodyText: 'hi' }),
      connection(),
    );
    expect(got.swaks).toContain('Content-Type: text/html; charset=utf-8');
    expect(got.swaks).toContain('<h1>hi</h1>');
    expect(got.message).toContain('Content-Type: text/html; charset=utf-8');
  });

  test('a text-only body stays text/plain', () => {
    const got = buildSmtpSnippet(values({ bodyHtml: '', bodyText: 'plain only' }), connection());
    expect(got.message).toContain('Content-Type: text/plain; charset=utf-8');
    expect(got.swaks).toContain("--body 'plain only'");
  });

  // Templates render server-side from a template_id the SMTP transport has no
  // way to carry. Saying so beats emitting a command that silently sends the
  // wrong body.
  test('a selected template is reported as unavailable rather than ignored', () => {
    const got = buildSmtpSnippet(values({ templateId: 'welcome-v2' }), connection());
    expect(got.notes.some((n) => n.includes('Templates are not available over SMTP'))).toBe(true);
  });

  test('the provider id appears as both the username and the header', () => {
    const got = buildSmtpSnippet(values({ providerId: 'abc-123' }), connection());
    expect(got.swaks).toContain("--auth-user 'abc-123'");
    expect(got.message).toContain('X-Panmail-Provider-Id: abc-123');
  });

  test('attachments are referenced by path and flagged', () => {
    const got = buildSmtpSnippet(
      values({ attachments: [{ filename: 'invoice.pdf' }] }),
      connection(),
    );
    expect(got.swaks).toContain("--attach '@/path/to/invoice.pdf'");
    expect(got.notes.some((n) => n.includes('real files'))).toBe(true);
  });

  test('an empty form still yields a runnable shape', () => {
    const got = buildSmtpSnippet(
      values({ providerId: '', from: '', to: [], subject: '', bodyText: '', bodyHtml: '' }),
      connection(),
    );
    expect(got.swaks).toContain('YOUR_PROVIDER_ID');
    expect(got.swaks).toContain('sender@example.com');
    expect(got.swaks).toContain('recipient@example.com');
    expect(got.message).toContain('Subject: (no subject)');
  });
});
