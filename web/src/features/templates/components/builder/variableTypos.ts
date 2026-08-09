/**
 * Flags variables that look like misspellings of other variables.
 *
 * A merge tag fails silently. `{{frist_name}}` is valid template syntax, the
 * renderer finds no value for it and substitutes nothing, and the message goes
 * out reading "Hi ," to everyone. Nothing in the pipeline objects, because
 * nothing can tell an intentional optional field from a typo.
 *
 * What can be told apart is a name used once that closely resembles a name used
 * repeatedly. That is the shape of a typo, and it is the only signal available
 * without knowing the caller's data — which the builder never does.
 */

export interface TypoSuspicion {
  /** The name that looks wrong. */
  variable: string;
  /** The established name it probably meant. */
  suggestion: string;
}

/**
 * Levenshtein distance, stopping once it exceeds `max`.
 *
 * Bounded because the answer is only ever compared against a small threshold,
 * and a template with many variables would otherwise compare every pair in
 * full on each keystroke.
 */
export const editDistance = (a: string, b: string, max: number): number => {
  if (a === b) return 0;
  if (Math.abs(a.length - b.length) > max) return max + 1;

  let previous = Array.from({ length: b.length + 1 }, (_, i) => i);

  for (let i = 1; i <= a.length; i++) {
    const current = [i];
    let rowMin = i;

    for (let j = 1; j <= b.length; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      const value = Math.min(
        current[j - 1] + 1,      // insertion
        previous[j] + 1,         // deletion
        previous[j - 1] + cost,  // substitution
      );
      current.push(value);
      if (value < rowMin) rowMin = value;
    }

    // Every remaining row can only grow, so the answer is already too large.
    if (rowMin > max) return max + 1;
    previous = current;
  }

  return previous[b.length];
};

/** How different two names may be and still be considered a slip. */
const threshold = (name: string): number => {
  // One edit on a short name is as likely to be a different variable as a
  // typo — `city` and `cite`, `id` and `ad`. Longer names earn more latitude
  // because the chance of two real variables landing that close falls away.
  if (name.length <= 4) return 0;
  if (name.length <= 8) return 1;
  return 2;
};

/**
 * How many uses make a name established rather than suspect.
 *
 * Two, not one: a template that uses `{{total}}` once and `{{totals}}` once
 * gives no basis for deciding which is wrong, and guessing would send the
 * author to correct the right one half the time.
 */
const ESTABLISHED_USES = 2;

/**
 * Finds likely misspellings among the variables a template uses.
 *
 * `counts` maps each variable name to how often the design refers to it.
 */
export const findTypos = (counts: Map<string, number>): TypoSuspicion[] => {
  const established: string[] = [];
  const suspects: string[] = [];

  for (const [name, uses] of counts) {
    if (uses >= ESTABLISHED_USES) established.push(name);
    else suspects.push(name);
  }

  const found: TypoSuspicion[] = [];

  for (const suspect of suspects) {
    const max = threshold(suspect);
    if (max === 0) continue;

    let best: string | undefined;
    let bestDistance = max + 1;

    for (const candidate of established) {
      // A name is never a typo of itself, and a dotted path resembling its own
      // root (`order` and `order.id`) is a real relationship, not a slip.
      if (candidate === suspect) continue;
      if (candidate.startsWith(`${suspect}.`) || suspect.startsWith(`${candidate}.`)) continue;

      const distance = editDistance(suspect, candidate, max);
      if (distance <= max && distance < bestDistance) {
        best = candidate;
        bestDistance = distance;
      }
    }

    if (best) found.push({ variable: suspect, suggestion: best });
  }

  return found.sort((a, b) => a.variable.localeCompare(b.variable));
};

/** Counts how often each variable name appears across the design's strings. */
export const countVariables = (design: unknown): Map<string, number> => {
  const counts = new Map<string, number>();
  const bump = (name: string) => counts.set(name, (counts.get(name) ?? 0) + 1);

  // A design loaded from storage is not guaranteed to be acyclic — the lint
  // suite carries a cyclic fixture for exactly this reason. Without a guard
  // this recursion blows the stack, and in the builder it would do so on every
  // keystroke, taking the tab with it.
  const seen = new WeakSet<object>();
  const scan = (value: unknown): void => {
    if (typeof value === 'string') {
      for (const match of value.matchAll(/\{\{\s*\.?([\w.]+)\s*\}\}/g)) {
        if (!RESERVED.has(match[1])) bump(match[1]);
      }
      for (const match of value.matchAll(HELPER_ARGUMENT)) {
        bump(match[1]);
      }
      return;
    }
    if (value && typeof value === 'object') {
      if (seen.has(value)) return;
      seen.add(value);
    }
    if (Array.isArray(value)) {
      value.forEach(scan);
      return;
    }
    if (value && typeof value === 'object') {
      for (const [key, inner] of Object.entries(value as Record<string, unknown>)) {
        if ((key === 'loopVariable' || key === 'ifVariable') && typeof inner === 'string') {
          const name = inner.trim();
          if (name && !RESERVED.has(name)) bump(name);
          continue;
        }
        scan(inner);
      }
    }
  };

  scan(design);
  return counts;
};

const RESERVED = new Set(['if', 'else', 'each', 'end', 'with', 'unless', 'this', 'range']);
const HELPER_ARGUMENT = /\{\{\s*#?\s*(?:if|unless|each|with|range)\s+\.?([\w.]+)\s*\}\}/g;
