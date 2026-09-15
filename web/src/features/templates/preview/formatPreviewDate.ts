/**
 * Renders a date the way the server's `.Format` does.
 *
 * A deliberate approximation, like the rest of this file: it covers the three
 * dialects the server accepts — Go's reference date, an example date, and the
 * YYYY-MM-DD tokens — because an author who writes one and sees raw template
 * syntax in the preview has no way to tell whether it works.
 */

/** Go's reference date, which is what its layouts are written as. */
const GO_REFERENCE: Record<string, (d: Date) => string> = {
  '2006': (d) => String(d.getUTCFullYear()),
  '06': (d) => String(d.getUTCFullYear()).slice(-2),
  January: (d) => MONTHS[d.getUTCMonth()],
  Jan: (d) => MONTHS[d.getUTCMonth()].slice(0, 3),
  Monday: (d) => DAYS[d.getUTCDay()],
  Mon: (d) => DAYS[d.getUTCDay()].slice(0, 3),
  '01': (d) => pad(d.getUTCMonth() + 1),
  '02': (d) => pad(d.getUTCDate()),
  '15': (d) => pad(d.getUTCHours()),
  '03': (d) => pad(hour12(d)),
  '04': (d) => pad(d.getUTCMinutes()),
  '05': (d) => pad(d.getUTCSeconds()),
  PM: (d) => (d.getUTCHours() < 12 ? 'AM' : 'PM'),
  pm: (d) => (d.getUTCHours() < 12 ? 'am' : 'pm'),
  '1': (d) => String(d.getUTCMonth() + 1),
  '2': (d) => String(d.getUTCDate()),
  '3': (d) => String(hour12(d)),
};

/** The YYYY-MM-DD dialect, expressed as the Go layout it stands for. */
const TOKENS: Array<[string, string]> = [
  ['YYYY', '2006'], ['YY', '06'],
  ['MMMM', 'January'], ['MMM', 'Jan'], ['MM', '01'], ['M', '1'],
  ['DDDD', 'Monday'], ['DDD', 'Mon'], ['DD', '02'], ['D', '2'],
  ['HH', '15'], ['hh', '03'], ['h', '3'],
  ['mm', '04'], ['m', '4'], ['ss', '05'], ['s', '5'],
  ['A', 'PM'], ['a', 'pm'],
];

const NAMED: Record<string, string> = {
  date: '2006-01-02',
  time: '15:04:05',
  datetime: '2006-01-02 15:04:05',
  iso: '2006-01-02T15:04:05Z',
  iso8601: '2006-01-02T15:04:05Z',
  rfc3339: '2006-01-02T15:04:05Z',
  kitchen: '3:04PM',
  short: 'Jan 2, 2006',
  long: 'January 2, 2006',
  human: 'Jan 2, 2006 3:04 PM',
};

const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July',
  'August', 'September', 'October', 'November', 'December'];
const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

const pad = (n: number): string => String(n).padStart(2, '0');
const hour12 = (d: Date): number => d.getUTCHours() % 12 || 12;

/** Ordered longest-first so "January" is not read as "Jan" followed by "uary". */
const GO_PARTS = Object.keys(GO_REFERENCE).sort((a, b) => b.length - a.length);

/**
 * Translates a layout to Go's, matching the server's resolution order: named,
 * then tokens, then an example date, then Go's own layout unchanged.
 */
const toGoLayout = (layout: string): string => {
  const named = NAMED[layout.trim().toLowerCase()];
  if (named) return named;

  const tokenised = asTokenLayout(layout);
  if (tokenised !== null) return tokenised;

  return asExampleLayout(layout) ?? layout;
};

/**
 * Reads "DD/MM/YYYY HH:mm", and returns null for anything else.
 *
 * All-or-nothing, like the server: one unrecognised letter rejects the whole
 * string, which is what keeps Go's own layouts — "Mon, 02 Jan 2006" — out.
 */
const asTokenLayout = (layout: string): string | null => {
  let out = '';
  let sawToken = false;

  for (let i = 0; i < layout.length; ) {
    if (!/[a-zA-Z]/.test(layout[i])) {
      out += layout[i];
      i += 1;
      continue;
    }
    const token = TOKENS.find(([t]) => layout.startsWith(t, i));
    if (!token) return null;
    out += token[1];
    i += token[0].length;
    sawToken = true;
  }

  return sawToken ? out : null;
};

/**
 * Reads an example date — "2026-12-01" — as the shape it is written in.
 *
 * Go's layout is its reference date, so writing the shape out with any other
 * date renders nonsense. The server reads such a layout as an example, and the
 * preview has to agree or it shows a date the recipient will not get.
 */
const asExampleLayout = (layout: string): string | null => {
  const candidates: Array<[RegExp, string]> = [
    [/^(\d{4})-(\d{2})-(\d{2})$/, '2006-01-02'],
    [/^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})$/, '2006-01-02 15:04'],
    [/^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})$/, '2006-01-02 15:04:05'],
    [/^(\d{4})\/(\d{2})\/(\d{2})$/, '2006/01/02'],
    [/^(\d{2})\/(\d{2})\/(\d{4})$/, '01/02/2006'],
    [/^(\d{2})-(\d{2})-(\d{4})$/, '02-01-2006'],
  ];

  for (const [re, go] of candidates) {
    const m = layout.match(re);
    if (!m) continue;
    // A slash date is ambiguous until a number rules one reading out: 25 can
    // only be a day. The server resolves it the same way round.
    if (go === '01/02/2006' && Number(m[1]) > 12) return '02/01/2006';
    // Keep the separator the author typed.
    return go.replace(/[-/ T]/g, (sep, at: number) => layout[at] ?? sep);
  }
  return null;
};

/** Applies a Go layout, leaving anything that is not a layout part alone. */
const applyGoLayout = (layout: string, when: Date): string => {
  let out = '';
  for (let i = 0; i < layout.length; ) {
    const part = GO_PARTS.find((p) => layout.startsWith(p, i));
    if (part) {
      out += GO_REFERENCE[part](when);
      i += part.length;
      continue;
    }
    out += layout[i];
    i += 1;
  }
  return out;
};

/**
 * Formats a value the server would accept as a date, or returns null so the
 * caller can show the author that this field is not one.
 *
 * A zone is applied by shifting the instant so that the UTC accessors above
 * read the target zone's wall clock — which is the whole point of showing it,
 * since the recipient reads a wall clock and not an instant.
 */
export const formatPreviewDate = (
  value: unknown,
  layout: string,
  zone?: string,
): string | null => {
  let when = asDate(value);
  if (!when) return null;

  if (zone) {
    when = shiftToZone(when, zone);
    if (!when) return null;
  }

  // With no layout at all the call was `.In "Asia/Jakarta"` on its own, which
  // Go prints as its own time format rather than as a date.
  if (!layout) return goTimeString(when, zone);
  return applyGoLayout(toGoLayout(layout), when);
};

/**
 * Rewrites an instant so its UTC fields hold the target zone's wall clock.
 *
 * Returns null for a zone Intl does not know, so the preview reports it the way
 * the server does rather than quietly falling back to UTC — a silent fallback
 * would show an hour nobody chose.
 */
const shiftToZone = (when: Date, zone: string): Date | null => {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: zone,
      hour12: false,
      year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    }).formatToParts(when);

    const at = (type: string) => Number(parts.find((p) => p.type === type)?.value);
    return new Date(Date.UTC(
      at('year'), at('month') - 1, at('day'),
      // Some engines render midnight as hour 24.
      at('hour') % 24, at('minute'), at('second'),
    ));
  } catch {
    return null;
  }
};

/** What Go prints for a time.Time with no layout. */
const goTimeString = (when: Date, zone?: string): string => {
  const offset = zone ? zoneOffset(when, zone) : '+0000';
  const name = zone ? zoneAbbreviation(when, zone) : 'UTC';
  return `${applyGoLayout('2006-01-02 15:04:05', when)} ${offset} ${name}`;
};

const zoneOffset = (when: Date, zone: string): string => {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'longOffset' })
    .formatToParts(when);
  const raw = parts.find((p) => p.type === 'timeZoneName')?.value ?? 'GMT+00:00';
  const m = raw.match(/([+-])(\d{2}):(\d{2})/);
  return m ? `${m[1]}${m[2]}${m[3]}` : '+0000';
};

const zoneAbbreviation = (when: Date, zone: string): string => {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'short' })
    .formatToParts(when);
  return parts.find((p) => p.type === 'timeZoneName')?.value ?? zone;
};

const asDate = (value: unknown): Date | null => {
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value;

  if (typeof value === 'number') {
    // Seconds or milliseconds, over the ranges where each can only mean one —
    // the server refuses anything between or outside them rather than guess.
    if (value >= 1e9 && value < 1e10) return new Date(value * 1000);
    if (value >= 1e12 && value < 1e13) return new Date(value);
    return null;
  }

  if (typeof value !== 'string') return null;
  // Date-only strings are UTC in the server's reading; Date agrees, but a
  // datetime without a zone does not, so it is pinned here.
  const text = /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2})?$/.test(value)
    ? `${value.replace(' ', 'T')}Z`
    : value;
  const parsed = new Date(text);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
};
