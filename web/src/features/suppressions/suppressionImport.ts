/**
 * Reads a suppression list exported from somewhere else.
 *
 * The file is whatever the previous provider produced, so the parser is
 * deliberately forgiving: one address per line, or `email,reason`, with or
 * without quotes and a header row. Anything it cannot read is handed back
 * rather than dropped, because the point of the import is that the operator
 * ends up knowing what happened to every line.
 *
 * Validation is not done here. The server parses addresses with the same
 * routine the send path uses, and a second opinion in the browser would only
 * disagree with it — this splits the file and leaves judgement to the gateway.
 */

export interface ParsedEntry {
  email: string;
  reason: string;
}

export interface ParsedList {
  entries: ParsedEntry[];
  /** Lines that held no address at all, with their 1-based line numbers. */
  skipped: number[];
}

/** Matches a header row so a column title is not imported as an address. */
const HEADER = /^"?\s*(e-?mail|address|recipient)\b/i;

/**
 * Splits one CSV line into fields.
 *
 * Quoted fields may contain commas, which is how a reason like
 * "bounced, mailbox full" survives. Doubled quotes inside a quoted field are
 * an escaped quote, per RFC 4180.
 */
function splitFields(line: string): string[] {
  const fields: string[] = [];
  let current = '';
  let quoted = false;

  for (let i = 0; i < line.length; i++) {
    const char = line[i];

    if (quoted) {
      if (char === '"') {
        if (line[i + 1] === '"') {
          current += '"';
          i++;
        } else {
          quoted = false;
        }
      } else {
        current += char;
      }
      continue;
    }

    if (char === '"') {
      quoted = true;
    } else if (char === ',') {
      fields.push(current);
      current = '';
    } else {
      current += char;
    }
  }
  fields.push(current);

  return fields.map((f) => f.trim());
}

export function parseSuppressionList(text: string): ParsedList {
  const entries: ParsedEntry[] = [];
  const skipped: number[] = [];

  // \r\n and \r both appear in files exported on Windows; splitting on \n
  // alone leaves a trailing \r on every address.
  const lines = text.split(/\r\n|\r|\n/);

  lines.forEach((line, index) => {
    const trimmed = line.trim();
    if (trimmed === '') return;

    // Only the first line can be a header. A later row matching the pattern
    // is far more likely to be an address at a domain like `email.example.com`.
    if (index === 0 && HEADER.test(trimmed)) return;

    const [email, ...rest] = splitFields(trimmed);
    if (!email) {
      skipped.push(index + 1);
      return;
    }

    entries.push({ email, reason: rest.join(', ').trim() });
  });

  return { entries, skipped };
}

/**
 * The server's own ceiling, mirrored so the console can split a large file
 * rather than being refused.
 *
 * Importing is idempotent, so sending a list as several requests is safe: a
 * chunk that is retried adds nothing the first attempt already wrote. Keep
 * this in step with MaxImportEntries in manage_suppressions_usecase.go.
 */
export const MAX_ENTRIES_PER_REQUEST = 10000;

export function chunkEntries<T>(entries: T[], size = MAX_ENTRIES_PER_REQUEST): T[][] {
  if (entries.length === 0) return [];
  const chunks: T[][] = [];
  for (let i = 0; i < entries.length; i += size) {
    chunks.push(entries.slice(i, i + size));
  }
  return chunks;
}

export interface ImportTotals {
  imported: number;
  alreadySuppressed: number;
  invalid: number;
  invalidSamples: string[];
}

/** Adds up the per-chunk answers into one result for the operator. */
export function totalise(
  results: Array<{
    imported: number;
    alreadySuppressed: number;
    invalid: number;
    invalidSamples: string[];
  }>,
): ImportTotals {
  const totals: ImportTotals = {
    imported: 0,
    alreadySuppressed: 0,
    invalid: 0,
    invalidSamples: [],
  };

  for (const r of results) {
    totals.imported += r.imported;
    totals.alreadySuppressed += r.alreadySuppressed;
    totals.invalid += r.invalid;
    // Each chunk caps its own samples; keeping every chunk's would grow with
    // the file, which is the thing the cap exists to prevent.
    for (const sample of r.invalidSamples) {
      if (totals.invalidSamples.length < 20) totals.invalidSamples.push(sample);
    }
  }

  return totals;
}

/** A one-line summary of what an import did. */
export function describeImport(totals: ImportTotals): string {
  const parts = [`${totals.imported} added`];
  if (totals.alreadySuppressed > 0) parts.push(`${totals.alreadySuppressed} already on the list`);
  if (totals.invalid > 0) parts.push(`${totals.invalid} unreadable`);
  return parts.join(', ');
}
