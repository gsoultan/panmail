import { describe, expect, test } from 'bun:test';
import { goApiSnippet, phpApiSnippet, javaApiSnippet, nodeApiSnippet } from './api';
import { goSdkSnippet, phpSdkSnippet, javaSdkSnippet, nodeSdkSnippet } from './sdk';
import { goSmtpSnippet, phpSmtpSnippet, javaSmtpSnippet, nodeSmtpSnippet } from './smtp';
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
  { name: 'Node', build: nodeApiSnippet },
];

const sdkGenerators = [
  { name: 'Go', build: goSdkSnippet },
  { name: 'PHP', build: phpSdkSnippet },
  { name: 'Java', build: javaSdkSnippet },
  { name: 'Node', build: nodeSdkSnippet },
];

// The two API-shaped tab groups share every generic assertion: same form
// values, same origin, same key from the environment.
const sendGenerators = [
  ...apiGenerators.map((g) => ({ ...g, name: `API ${g.name}` })),
  ...sdkGenerators.map((g) => ({ ...g, name: `SDK ${g.name}` })),
];

const smtpGenerators = [
  { name: 'Go', build: goSmtpSnippet },
  { name: 'PHP', build: phpSmtpSnippet },
  { name: 'Java', build: javaSmtpSnippet },
  { name: 'Node', build: nodeSmtpSnippet },
];

/**
 * Generated code is pasted into real projects, so the bar is that it compiles
 * and does what the form said. The two things worth pinning are that a value
 * a user typed cannot break out of the string literal holding it, and that a
 * Bcc stays blind.
 */
describe('API and SDK snippets', () => {
  for (const { name, build } of sendGenerators) {
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

  test('each SDK snippet uses its published client', () => {
    expect(goSdkSnippet(values(), BASE)).toContain('github.com/gsoultan/panmail-sdk');
    expect(phpSdkSnippet(values(), BASE)).toContain('use Panmail\\Client;');
    expect(javaSdkSnippet(values(), BASE)).toContain('io.github.gsoultan.panmail.PanmailClient');
    expect(nodeSdkSnippet(values(), BASE)).toContain("from '@gsoultan/panmail-sdk'");
  });

  // The client is given a base URL and derives the procedure path itself, so an
  // SDK snippet that spells the path out has stopped going through the client.
  test('no SDK snippet names the procedure path itself', () => {
    for (const { name, build } of sdkGenerators) {
      expect(build(values(), BASE), name).not.toContain('/panmail.v1.EmailService/SendEmail');
    }
  });

  // The API tab is the one that shows the endpoint, because holding the HTTP
  // yourself is the whole point of it.
  test('every API snippet posts to the send endpoint', () => {
    for (const { name, build } of apiGenerators) {
      expect(build(values(), BASE), name).toContain(
        'https://mail.example.com/panmail.v1.EmailService/SendEmail',
      );
    }
  });

  // The API snippets set the header themselves; the SDKs do it internally.
  test('every API snippet sends the key as X-API-Key', () => {
    for (const { name, build } of apiGenerators) {
      expect(build(values(), BASE), name).toContain('X-API-Key');
    }
  });

  // Authorization carries a dashboard session, and a key sent as a bearer
  // token is rejected as a malformed session rather than as a bad key.
  test('no snippet sets an Authorization header', () => {
    for (const { name, build } of sendGenerators) {
      const code = build(values(), BASE);
      expect(code, name).not.toContain("'Authorization:");
      expect(code, name).not.toContain('"Authorization"');
    }
  });

  // Both capacity refusals answer 429 and only one carries a Retry-After. A
  // snippet that treats them alike teaches the wrong lesson — and the wrong
  // lesson here is an immediate retry against a queue that is already too deep.
  test('each SDK snippet catches the two capacity refusals separately', () => {
    for (const { name, build } of sdkGenerators) {
      const code = build(values(), BASE);
      expect(code, name).toContain('RateLimited');
      expect(code, name).toContain('BacklogFull');
    }
  });

  test('each API snippet tells the two 429s apart by Retry-After', () => {
    for (const { name, build } of apiGenerators) {
      const code = build(values(), BASE);
      expect(code, name).toContain('429');
      expect(code, name).toContain('Retry-After');
      expect(code, name).toContain('the queue is too deep');
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
    const code = goSdkSnippet(values({ subject: nasty }), BASE);
    const line = code.split('\n').find((l) => l.includes('Subject:'))!;
    expect(line).toContain('\\"hi\\"');
    expect(line).toContain('\\\\');
    // The literal must stay on one line.
    expect(line).toContain('\\n');
  });

  test('PHP escapes the single quote that would close its literal', () => {
    const code = phpSdkSnippet(values({ subject: nasty }), BASE);
    expect(code).toContain("it\\'s");
  });

  test('Java escapes quotes and newlines', () => {
    const code = javaSdkSnippet(values({ subject: nasty }), BASE);
    const line = code.split('\n').find((l) => l.includes('.subject('))!;
    expect(line).toContain('\\"hi\\"');
    expect(line).toContain('\\n');
  });

  test('Node escapes the single quote that would close its literal', () => {
    const code = nodeSdkSnippet(values({ subject: nasty }), BASE);
    const line = code.split('\n').find((l) => l.includes('subject:'))!;
    expect(line).toContain("it\\'s");
    // The literal must stay on one line.
    expect(line).toContain('\\n');
  });

  // The per-language tests above pin the exact escape each one needs. This is
  // the invariant behind all of them, checked on both tab groups: if the text a
  // user typed survives into the snippet untouched, nothing escaped it.
  test('no generator emits the raw text verbatim', () => {
    for (const { name, build } of sendGenerators) {
      const code = build(values({ subject: nasty }), BASE);
      expect(code, name).not.toContain(nasty);
      expect(code, name).not.toContain('He said "hi" it\'s');
    }
  });

  test('a backtick in the body cannot close the Go raw literal', () => {
    const code = goSmtpSnippet(values({ bodyText: 'a ` b' }), connection());
    expect(code).toContain('` + "`" + `');
  });
});
