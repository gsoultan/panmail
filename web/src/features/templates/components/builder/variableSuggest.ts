/**
 * Works out whether the caret is inside a merge tag being typed.
 *
 * Inserting a variable used to mean stopping, opening a popover, finding the
 * name in a list and clicking it — which is slower than typing `{{name}}` by
 * hand, so the list mostly went unused and names got misspelled instead. This
 * makes typing the fast path: `{{` opens the suggestions, and they narrow as
 * the name is typed.
 *
 * The caret arithmetic lives here rather than in the component because it is
 * the part that is easy to get subtly wrong — off by one, or confused by a
 * second tag on the same line — and those mistakes corrupt the author's text
 * rather than merely looking wrong.
 */

export interface ActiveToken {
  /** Index of the opening brace pair. */
  start: number;
  /** What has been typed after `{{`, used to filter. */
  query: string;
  /** Index just past the closing `}}`, when the tag is already closed. */
  end: number;
}

const OPEN = '{{';
const CLOSE = '}}';

/**
 * Finds the tag the caret sits in, or null.
 *
 * Scans back from the caret for an unclosed `{{`. A newline ends the search: a
 * brace pair left open on an earlier line is a typo, not an invitation to
 * autocomplete every subsequent word.
 */
export const activeToken = (text: string, caret: number): ActiveToken | null => {
  const before = text.slice(0, caret);

  const open = before.lastIndexOf(OPEN);
  if (open === -1) return null;

  const between = before.slice(open + OPEN.length);

  // Already closed before the caret, so the caret is outside the tag.
  if (between.includes(CLOSE)) return null;
  if (between.includes('\n')) return null;

  // A name, a dotted path, or the beginning of either. Anything else means the
  // braces were not the start of a merge tag.
  if (!/^[\w.]*$/.test(between)) return null;

  // Consume a closing pair that is already there, so retyping inside an
  // existing tag replaces it rather than nesting a second one inside it.
  const after = text.slice(caret);
  const closeAt = after.startsWith(CLOSE) ? caret + CLOSE.length : caret;

  return { start: open, query: between, end: closeAt };
};

/** Ranks the variables worth offering for what has been typed so far. */
export const filterVariables = (variables: string[], query: string): string[] => {
  const q = query.toLowerCase();
  if (!q) return variables;

  const starts: string[] = [];
  const contains: string[] = [];
  for (const v of variables) {
    const lower = v.toLowerCase();
    if (lower.startsWith(q)) starts.push(v);
    else if (lower.includes(q)) contains.push(v);
  }
  // A prefix match is what someone typing a name they know is aiming at; a
  // substring match is a fallback for a half-remembered one.
  return [...starts, ...contains];
};

export interface Insertion {
  text: string;
  /** Where the caret belongs afterwards: past the closing braces. */
  caret: number;
}

/** Replaces the tag being typed with the chosen variable. */
export const insertVariable = (
  text: string,
  token: ActiveToken,
  variable: string,
): Insertion => {
  const tag = `${OPEN}${variable}${CLOSE}`;
  return {
    text: text.slice(0, token.start) + tag + text.slice(token.end),
    caret: token.start + tag.length,
  };
};

/**
 * Every variable a design already refers to.
 *
 * Offering only a fixed built-in list meant the second use of a project's own
 * variable was typed from memory, which is how `{{order_id}}` and
 * `{{orderId}}` end up in the same template. Anything used once is offered
 * everywhere after that.
 */
export const variablesInDesign = (design: unknown): string[] => {
  const found = new Set<string>();

  const scan = (value: unknown): void => {
    if (typeof value === 'string') {
      for (const match of value.matchAll(/\{\{\s*[#/]?\s*\.?([\w.]+)\s*\}\}/g)) {
        const name = match[1];
        if (!RESERVED.has(name)) found.add(name);
      }
      // A helper takes its subject as an argument, so `{{#if vip}}` never
      // matches the pattern above and `vip` would go unoffered even though the
      // template plainly depends on it.
      for (const match of value.matchAll(HELPER_ARGUMENT)) {
        found.add(match[1]);
      }
      return;
    }
    if (Array.isArray(value)) {
      value.forEach(scan);
      return;
    }
    if (value && typeof value === 'object') {
      for (const [key, inner] of Object.entries(value as Record<string, unknown>)) {
        // The loop and conditional fields hold a bare variable name rather
        // than a tag, so they never match the pattern above.
        if ((key === 'loopVariable' || key === 'ifVariable') && typeof inner === 'string') {
          const name = inner.trim();
          if (name && !RESERVED.has(name)) found.add(name);
          continue;
        }
        scan(inner);
      }
    }
  };

  scan(design);
  return [...found].sort();
};

// Handlebars' own keywords, which are not the author's variables.
const RESERVED = new Set(['if', 'else', 'each', 'end', 'with', 'unless', 'this', 'range']);

// `{{#if vip}}`, `{{#each items}}`, and the Go-style `{{if .vip}}` /
// `{{range .items}}` the templates also accept.
const HELPER_ARGUMENT = /\{\{\s*#?\s*(?:if|unless|each|with|range)\s+\.?([\w.]+)\s*\}\}/g;
