// The subset of the send form that any generated snippet needs. Kept separate
// from the form's own type so a new form field cannot silently change what the
// snippets claim to send.
export interface SnippetValues {
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

export const PLACEHOLDER_KEY = 'YOUR_API_KEY';
export const PLACEHOLDER_PROVIDER = 'YOUR_PROVIDER_ID';
export const PLACEHOLDER_HOST = 'YOUR_PANMAIL_HOST';

// Every generator interpolates form values — free text a user typed — into
// source code. Each language gets its own escaper rather than one shared
// "escape quotes" helper, because the languages disagree about what needs
// escaping and a snippet that will not compile is worse than no snippet.

/** Go interpreted string literal contents. */
export function goString(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\r/g, '\\r')
    .replace(/\t/g, '\\t');
}

/** PHP single-quoted string contents, where only \ and ' are special. */
export function phpString(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/'/g, "\\'");
}

/** Java string literal contents. */
export function javaString(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\r/g, '\\r')
    .replace(/\t/g, '\\t');
}

/** A Go []string literal. */
export function goStringSlice(values: string[]): string {
  return `[]string{${values.map((v) => `"${goString(v)}"`).join(', ')}}`;
}

/** A PHP array literal. */
export function phpArray(values: string[]): string {
  return `[${values.map((v) => `'${phpString(v)}'`).join(', ')}]`;
}

/** A Java List.of(...) literal. */
export function javaList(values: string[]): string {
  return `List.of(${values.map((v) => `"${javaString(v)}"`).join(', ')})`;
}

/** The address lists, with the fallbacks the snippets show for an empty form. */
export function resolved(values: SnippetValues) {
  return {
    providerId: values.providerId || PLACEHOLDER_PROVIDER,
    from: values.from || 'sender@example.com',
    to: values.to?.length ? values.to : ['recipient@example.com'],
    cc: values.cc ?? [],
    bcc: values.bcc ?? [],
    subject: values.subject || '(no subject)',
    bodyHtml: values.bodyHtml ?? '',
    bodyText: values.bodyText ?? '',
  };
}

/** Envelope order: every address that receives a copy, blind or not, once. */
export function envelopeAddresses(...lists: string[][]): string[] {
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
