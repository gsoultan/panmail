import type { SmtpConnection } from './smtpConnection';

// What the send form holds, narrowed to the parts that survive a trip through
// SMTP. Anything the protocol cannot carry is reported in `notes` rather than
// quietly dropped from the command.
export interface SmtpSnippetValues {
  providerId: string;
  from: string;
  to: string[];
  cc: string[];
  bcc: string[];
  subject: string;
  bodyHtml: string;
  bodyText: string;
  templateId?: string;
  attachments?: { filename: string }[];
}

export interface SmtpSnippet {
  swaks: string;
  message: string;
  // Things that are true of this particular message and would otherwise be a
  // surprise at the far end.
  notes: string[];
}

const PLACEHOLDER_HOST = 'YOUR_PANMAIL_HOST';
const PLACEHOLDER_PROVIDER = 'YOUR_PROVIDER_ID';
const PLACEHOLDER_KEY = 'YOUR_API_KEY';

// shellQuote wraps a value for a POSIX shell. Subjects and bodies are free
// text, and an apostrophe in one would otherwise end the quoting and produce a
// command that fails in a way nobody would connect back to this panel.
function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

// uniqueAddresses lower-cases and de-duplicates while preserving order, the
// same way the gateway does before it counts recipients.
function uniqueAddresses(...lists: string[][]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const list of lists) {
    for (const raw of list ?? []) {
      const address = raw.trim();
      if (!address) continue;
      const key = address.toLowerCase();
      if (seen.has(key)) continue;
      seen.add(key);
      out.push(address);
    }
  }
  return out;
}

/**
 * buildSmtpSnippet renders the same message as an SMTP submission.
 *
 * The one thing worth being careful about is Bcc. Over SMTP the envelope and
 * the headers are separate: a blind recipient belongs in RCPT TO and nowhere
 * else, so it appears in the swaks `--to` list but never in the message. A
 * snippet that put it in a header would teach exactly the mistake the gateway
 * goes out of its way to prevent.
 */
export function buildSmtpSnippet(
  values: SmtpSnippetValues,
  connection: SmtpConnection,
): SmtpSnippet {
  const host = connection.host || PLACEHOLDER_HOST;
  const port = connection.port || 587;
  const provider = values.providerId || PLACEHOLDER_PROVIDER;

  const from = values.from || 'sender@example.com';
  const to = values.to?.length ? values.to : ['recipient@example.com'];
  const cc = values.cc ?? [];
  const bcc = values.bcc ?? [];

  // Every address that should receive a copy, blind or not.
  const envelope = uniqueAddresses(to, cc, bcc);

  const isHtml = Boolean(values.bodyHtml?.trim());
  const body = isHtml ? values.bodyHtml : values.bodyText;
  const subject = values.subject || '(no subject)';

  const swaksLines = [
    `swaks --server ${host}:${port}`,
    `--auth-user ${shellQuote(provider)}`,
    `--auth-password ${shellQuote(PLACEHOLDER_KEY)}`,
  ];

  // --tls is STARTTLS in swaks, and it fails rather than falling back, which
  // is what you want from a command whose password is an API key.
  if (connection.encryption === 'STARTTLS') {
    swaksLines.push('--tls');
  }

  swaksLines.push(`--from ${shellQuote(from)}`);
  swaksLines.push(`--to ${shellQuote(envelope.join(','))}`);
  swaksLines.push(`--header ${shellQuote(`Subject: ${subject}`)}`);

  // Only the visible lists become headers. Bcc is already in --to above.
  if (to.length > 0) {
    swaksLines.push(`--header ${shellQuote(`To: ${to.join(', ')}`)}`);
  }
  if (cc.length > 0) {
    swaksLines.push(`--header ${shellQuote(`Cc: ${cc.join(', ')}`)}`);
  }
  if (isHtml) {
    swaksLines.push(`--add-header ${shellQuote('Content-Type: text/html; charset=utf-8')}`);
  }
  swaksLines.push(`--body ${shellQuote(body || '')}`);

  for (const attachment of values.attachments ?? []) {
    swaksLines.push(`--attach ${shellQuote(`@/path/to/${attachment.filename}`)}`);
  }

  const headerLines = [
    `From: ${from}`,
    `To: ${to.join(', ')}`,
    ...(cc.length > 0 ? [`Cc: ${cc.join(', ')}`] : []),
    `Subject: ${subject}`,
    // The header form of the provider, shown because it is the only way to
    // reach a second provider on one authenticated connection.
    `X-Panmail-Provider-Id: ${provider}`,
    `Content-Type: ${isHtml ? 'text/html' : 'text/plain'}; charset=utf-8`,
  ];

  const message = `${headerLines.join('\n')}\n\n${body || ''}`;

  const notes: string[] = [];
  if (bcc.length > 0) {
    notes.push(
      'Bcc recipients are in the envelope only. They are addressed with RCPT TO and never appear in the message, which is what keeps them blind.',
    );
  }
  if (values.templateId) {
    notes.push(
      'Templates are not available over SMTP. The message body is whatever you submit, so this command sends the content above rather than rendering the selected template.',
    );
  }
  if ((values.attachments?.length ?? 0) > 0) {
    notes.push(
      'Attachments are referenced by path. Point --attach at the real files before running the command.',
    );
  }
  if (connection.encryption !== 'STARTTLS') {
    notes.push(
      'This listener does not offer STARTTLS, so the API key would cross the network in the clear. Use it only where the hop is already private.',
    );
  }

  return { swaks: swaksLines.join(' \\\n  '), message, notes };
}
