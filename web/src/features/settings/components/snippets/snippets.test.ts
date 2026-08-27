import { describe, expect, test } from 'bun:test';
import { goApiSnippet, phpApiSnippet, javaApiSnippet } from './api';
import { goSmtpSnippet, phpSmtpSnippet, javaSmtpSnippet } from './smtp';
import type { SnippetValues } from './types';
import type { SmtpConnection } from '../smtpConnection';

const BASE = 'https://mail.example.com';

const connection = (over: Partial<SmtpConnection> = {}): SmtpConnection => ({
  enabled: true,
  host: 'mail.example.com',
  hostSource: 'listener',
  port: 587,
  encryption: 'STARTTLS',
  insecureAuthAllowed: false,
  ...over,
});

const values = (over: Partial<SnippetValues> = {}): SnippetValues => ({
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

const apiGenerators = [
  { name: 'Go', build: goApiSnippet },
  { name: 'PHP', build: phpApiSnippet },
  { name: 'Java', build: javaApiSnippet },
];

const smtpGenerators = [
  { name: 'Go', build: goSmtpSnippet },
  { name: 'PHP', build: phpSmtpSnippet },
  { name: 'Java', build: javaSmtpSnippet },
];

/**
 * Generated code is pasted into real projects, so the bar is that it compiles
 * and does what the form said. The two things worth pinning are that a value
 * a user typed cannot break out of the string literal holding it, and that a
 * Bcc stays blind.
 */
describe('API snippets', () => {
  for (const { name, build } of apiGenerators) {
    describe(name, () => {
      test('carries the form values', () => {
        const code = build(values(), BASE);
        expect(code).toContain('3f1c2b7a-0000-4000-8000-000000000001');
        expect(code).toContain('app@example.com');
        expect(code).toContain('user@example.net');
        expect(code).toContain('Hello');
      });

      test('targets this origin', () => {
        expect(build(values(), BASE)).toContain('https://mail.example.com');
      });

      test('reads the key from the environment rather than inlining it', () => {
        const code = build(values(), BASE);
        expect(code).toContain('PANMAIL_API_KEY');
      });

      test('omits cc and bcc when the form has none', () => {
        const code = build(values(), BASE);
        expect(code.toLowerCase()).not.toContain("'cc'");
        expect(code).not.toContain('Cc:');
      });

      test('includes cc and bcc when the form has them', () => {
        const code = build(values({ cc: ['c@example.net'], bcc: ['b@example.net'] }), BASE);
        expect(code).toContain('c@example.net');
        expect(code).toContain('b@example.net');
      });
    });
  }

  // The API takes bcc as its own field, so the gateway keeps it out of the
  // headers. That is the opposite of SMTP and worth not confusing.
  test('Go uses the published client rather than hand-rolled HTTP', () => {
    const code = goApiSnippet(values(), BASE);
    expect(code).toContain('github.com/gsoultan/panmail/pkg/panmail');
    expect(code).toContain('client.Send(context.Background()');
  });

  // The hand-rolled clients name the endpoint themselves; the Go client is
  // given a base URL and derives the path.
  test('the hand-rolled clients post to the send endpoint', () => {
    for (const build of [phpApiSnippet, javaApiSnippet]) {
      expect(build(values(), BASE)).toContain(
        'https://mail.example.com/panmail.v1.EmailService/SendEmail',
      );
    }
  });

  // Authorization carries a dashboard session, and a key sent as a bearer
  // token is rejected as a malformed session rather than as a bad key. The
  // snippets say so in a comment, so this checks it is not used as a header.
  test('the API key goes in X-API-Key, never as an Authorization header', () => {
    for (const build of [phpApiSnippet, javaApiSnippet]) {
      const code = build(values(), BASE);
      expect(code).toContain('X-API-Key');
      expect(code).not.toContain("'Authorization:");
      expect(code).not.toContain('"Authorization"');
    }
  });
});

describe('SMTP snippets', () => {
  for (const { name, build } of smtpGenerators) {
    describe(name, () => {
      test('dials the reported host and port', () => {
        const code = build(values(), connection({ host: 'smtp.internal', port: 2525 }));
        expect(code).toContain('smtp.internal');
        expect(code).toContain('2525');
      });

      test('signs in with the provider id and an API key from the environment', () => {
        const code = build(values(), connection());
        expect(code).toContain('3f1c2b7a-0000-4000-8000-000000000001');
        expect(code).toContain('PANMAIL_API_KEY');
      });

      test('a blind recipient never lands in a To or Cc header', () => {
        const code = build(
          values({ to: ['visible@example.net'], cc: ['copied@example.net'], bcc: ['blind@example.net'] }),
          connection(),
        );
        // It has to be addressed, or it never arrives.
        expect(code).toContain('blind@example.net');
        // But never on a header line that other recipients would read.
        for (const line of code.split('\n')) {
          const header = line.match(/"(To|Cc):\s*([^"]*)"/) ?? line.match(/'(To|Cc):\s*([^']*)'/);
          if (header) expect(header[2]).not.toContain('blind@example.net');
        }
      });

      test('falls back to a placeholder host when the listener is off', () => {
        const code = build(values(), connection({ enabled: false, host: '', port: 0, encryption: 'None' }));
        expect(code).toContain('YOUR_PANMAIL_HOST');
      });
    });
  }

  // Go hands you the envelope and the headers separately, so its snippet is
  // the one that can get Bcc wrong. It builds the header list explicitly.
  test('Go puts every recipient in the envelope and only visible ones in headers', () => {
    const code = goSmtpSnippet(
      values({ to: ['a@example.net'], cc: ['c@example.net'], bcc: ['b@example.net'] }),
      connection(),
    );
    expect(code).toContain('rcpt := []string{"a@example.net", "c@example.net", "b@example.net"}');
    expect(code).toContain('"To: a@example.net",');
    expect(code).toContain('"Cc: c@example.net",');
    expect(code).not.toContain('"To: a@example.net, b@example.net"');
  });

  test('STARTTLS is required when offered and flagged when not', () => {
    expect(javaSmtpSnippet(values(), connection())).toContain('mail.smtp.starttls.required');

    const plain = javaSmtpSnippet(values(), connection({ encryption: 'None' }));
    expect(plain).not.toContain('mail.smtp.starttls.required');
    expect(plain).toContain('in the clear');
  });

  test('PHPMailer is told whether the body is html', () => {
    expect(phpSmtpSnippet(values({ bodyHtml: '<h1>hi</h1>' }), connection())).toContain(
      '$mail->isHTML(true)',
    );
    expect(phpSmtpSnippet(values({ bodyHtml: '' }), connection())).toContain('$mail->isHTML(false)');
  });
});

/**
 * A subject or body is free text that lands inside a string literal. Each
 * language disagrees about what closes one, so each is checked on its own
 * terms: a snippet that will not compile is worse than no snippet.
 */
describe('injection into string literals', () => {
  const nasty = `He said "hi" it's \\ over
newline`;

  test('Go escapes quotes, backslashes and newlines', () => {
    const code = goApiSnippet(values({ subject: nasty }), BASE);
    const line = code.split('\n').find((l) => l.includes('Subject:'))!;
    expect(line).toContain('\\"hi\\"');
    expect(line).toContain('\\\\');
    // The literal must stay on one line.
    expect(line).toContain('\\n');
  });

  test('PHP escapes the single quote that would close its literal', () => {
    const code = phpApiSnippet(values({ subject: nasty }), BASE);
    expect(code).toContain("it\\'s");
  });

  test('Java escapes quotes and newlines', () => {
    const code = javaApiSnippet(values({ subject: nasty }), BASE);
    const line = code.split('\n').find((l) => l.includes('"subject"'))!;
    expect(line).toContain('\\"hi\\"');
    expect(line).toContain('\\n');
  });

  test('a backtick in the body cannot close the Go raw literal', () => {
    const code = goSmtpSnippet(values({ bodyText: 'a ` b' }), connection());
    expect(code).toContain('` + "`" + `');
  });
});
