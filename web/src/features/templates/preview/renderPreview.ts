/**
 * Renders a template the way the preview needs to show it.
 *
 * This is an approximation of the server's Handlebars renderer, not a
 * reimplementation of it — the point is to answer "what will this look like",
 * not "what will this produce". Where the two could disagree the preview is
 * deliberately the more forgiving one, because a preview that refuses to render
 * an unfinished template is useless while it is being written.
 *
 * The important change over the previous behaviour is sample data. A preview
 * that shows `[name]` where a name goes cannot tell you anything about the thing
 * that actually breaks layouts: real values are not the length of their
 * placeholders. "Hi [name]" and "Hi Alexandra Featherstonehaugh-Smythe" wrap
 * differently, overflow differently, and look nothing alike in a narrow column.
 * With sample data supplied, the preview renders what a recipient sees.
 */

import { formatPreviewDate } from './formatPreviewDate';

export interface PreviewData {
  [key: string]: unknown;
}

/** How many times a loop repeats when there is no data to iterate. */
const PLACEHOLDER_LOOP_REPEATS = 2;

/** Guards against a template whose blocks never close. */
const MAX_PASSES = 5;

/** Guards against a design that nests loops without bound. */
const MAX_DEPTH = 8;

const HANDLEBARS_KEYWORDS = new Set([
  'if', 'else', 'each', 'range', 'end', 'with', 'unless', 'this',
]);

/** Resolves "order.customer.name" against the sample data. */
const lookup = (data: PreviewData | undefined, path: string): unknown => {
  if (!data) return undefined;
  if (path === 'this' || path === '.') return data;

  let current: unknown = data;
  for (const part of path.split('.')) {
    if (part === '') continue;
    if (current === null || typeof current !== 'object') return undefined;
    current = (current as Record<string, unknown>)[part];
  }
  return current;
};

/**
 * Handlebars' notion of falsy, which is not JavaScript's.
 *
 * An empty array is falsy in Handlebars and truthy in JavaScript, and getting
 * that wrong is the difference between a preview that shows an empty "your
 * items" section and one that correctly hides it.
 */
const isTruthy = (value: unknown): boolean => {
  if (value === undefined || value === null || value === false) return false;
  if (value === 0 || value === '') return false;
  if (Array.isArray(value)) return value.length > 0;
  return true;
};

const escapeHtml = (value: unknown): string =>
  String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');

/** Splits a block body on a top-level {{else}}. */
const splitOnElse = (body: string): [string, string] => {
  const m = body.match(/\{\{\s*else\s*\}\}/);
  if (!m || m.index === undefined) return [body, ''];
  return [body.slice(0, m.index), body.slice(m.index + m[0].length)];
};

const EACH_RE = /\{\{\s*#each\s+([\w.]+)\s*\}\}([\s\S]*?)\{\{\s*\/each\s*\}\}/;
const RANGE_RE = /\{\{\s*range\s+\.?([\w.]+)\s*\}\}([\s\S]*?)\{\{\s*end\s*\}\}/;
const IF_RE = /\{\{\s*#if\s+([\w.]+)\s*\}\}([\s\S]*?)\{\{\s*\/if\s*\}\}/;
const GO_IF_RE = /\{\{\s*if\s+\.?([\w.]+)\s*\}\}([\s\S]*?)\{\{\s*end\s*\}\}/;
const UNLESS_RE = /\{\{\s*#unless\s+([\w.]+)\s*\}\}([\s\S]*?)\{\{\s*\/unless\s*\}\}/;

/** An action calling a date method, in every spelling the server takes. */
const DATE_ACTION_RE = /\{\{([^{}]*\.(?:[Ff]ormat|In)[^{}]*)\}\}/g;

/** The first quoted string at or after `from`, in any of the three quotes. */
const quotedAfter = (text: string, from: number): { value: string; end: number } | null => {
  const m = /"([^"]*)"|'([^']*)'|`([^`]*)`/.exec(text.slice(from));
  if (!m) return null;
  return { value: m[1] ?? m[2] ?? m[3], end: from + m.index + m[0].length };
};

/** The field the method is called on, which is the token just before it. */
const pathBefore = (text: string, at: number): string | null => {
  const m = /(\$?[\w.]+)$/.exec(text.slice(0, at));
  return m ? m[1].replace(/^\./, '') : null;
};

interface DateCall {
  path: string;
  layout: string;
  zone?: string;
}

/**
 * Reads a date call out of an action.
 *
 * Small parser rather than one regex because the forms compose: `.In` may carry
 * its zone bare or wrapped in Go's `(time.LoadLocation …)`, `.Format` may carry
 * a zone as a second argument, and the two may be chained. Taking "the first
 * quoted string after the method" handles the wrapper for free — it is the zone
 * either way.
 */
const parseDateCall = (inner: string): DateCall | null => {
  let path: string | null = null;
  let zone: string | undefined;
  let layout = '';

  const inAt = inner.search(/\.In\s/);
  if (inAt >= 0) {
    path = pathBefore(inner, inAt);
    zone = quotedAfter(inner, inAt)?.value;
    if (!zone) return null;
  }

  const formatAt = inner.search(/\.[Ff]ormat\s/);
  if (formatAt >= 0) {
    path = path ?? pathBefore(inner, formatAt);
    const first = quotedAfter(inner, formatAt);
    if (!first) return null;
    layout = first.value;
    // A second argument to Format is a zone, unless .In already gave one.
    zone = zone ?? quotedAfter(inner, first.end)?.value;
  }

  if (!path || (inAt < 0 && formatAt < 0)) return null;
  return { path, layout, zone };
};

/**
 * Renders the dates.
 *
 * Without this the author sees `{{ created_at.Format "2026-12-01" }}` verbatim,
 * which answers neither question a preview is for: whether the date is right,
 * and whether it fits. A field that is not a date, or a zone that is not a
 * zone, is called out rather than named like a missing variable, because the
 * server refuses to send that template at all — a preview that looked merely
 * incomplete would hide a failed send.
 */
const renderDates = (
  html: string,
  scope: PreviewData | undefined,
  root: PreviewData | undefined,
): string =>
  html.replace(DATE_ACTION_RE, (match, inner: string) => {
    const call = parseDateCall(inner);
    if (!call) return match;

    const value = lookup(scope, call.path) ?? lookup(root, call.path);
    if (value === undefined || value === null) return `[${call.path}]`;

    const formatted = formatPreviewDate(value, call.layout, call.zone);
    if (formatted === null) return `[${call.path} is not a date]`;
    return escapeHtml(formatted);
  });

/**
 * Replaces the remaining {{value}} expressions.
 *
 * `scope` is the current loop item when inside one and `root` the whole data
 * set, so `{{name}}` and `{{brand}}` both resolve from inside a loop — the item
 * first, then the root.
 */
const renderScalars = (
  html: string,
  scope: PreviewData | undefined,
  root: PreviewData | undefined,
): string =>
  html.replace(/\{\{\s*\.?([\w.]+)\s*\}\}/g, (_match, name: string) => {
    if (HANDLEBARS_KEYWORDS.has(name)) {
      // `this` inside a loop over scalars is the item itself.
      if (name === 'this' && scope !== undefined && typeof scope !== 'object') {
        return escapeHtml(scope);
      }
      return '';
    }

    const fromScope = lookup(scope, name);
    if (fromScope !== undefined) return escapeHtml(fromScope);

    const fromRoot = lookup(root, name);
    if (fromRoot !== undefined) return escapeHtml(fromRoot);

    // Nothing to substitute: name the variable rather than leaving a gap, so an
    // author can see which one has no value.
    return `[${name}]`;
  });

/**
 * Expands loops and conditionals, then substitutes what is left.
 *
 * Recursive rather than iterative over the whole document, because a loop body
 * has its own scope: a conditional inside a loop has to be decided against the
 * current item, and the item is only known while expanding that loop. Expanding
 * everything in flat passes evaluated inner blocks against the wrong data.
 */
const render = (
  html: string,
  scope: PreviewData | undefined,
  root: PreviewData | undefined,
  depth = 0,
): string => {
  if (depth > MAX_DEPTH) return html;

  let out = html;

  for (let pass = 0; pass < MAX_PASSES; pass++) {
    const before = out;

    const expandLoop = (re: RegExp) => {
      out = out.replace(re, (_m, path: string, rawBody: string) => {
        // A loop may carry an {{else}} branch for the empty case — an empty
        // cart, a digest with no stories. Ignoring it rendered the empty text
        // alongside the items, which is the one combination that can never be
        // correct.
        const [body, whenEmpty] = splitOnElse(rawBody);
        const value = lookup(scope, path) ?? lookup(root, path);

        if (Array.isArray(value)) {
          // An empty array takes the else branch, which is the whole point of
          // being able to preview an empty state. Each item becomes the scope
          // for the body, so nested blocks see the right values.
          if (value.length === 0) {
            return whenEmpty ? render(whenEmpty, scope, root, depth + 1) : '';
          }
          return value
            .map((item) => render(body, item as PreviewData, root, depth + 1))
            .join('');
        }

        // Without data, repeat so the reader can see it is a repeating region.
        // The else branch stays hidden: it is the exception, and showing both
        // at once would misrepresent every render.
        return render(body, scope, root, depth + 1).repeat(PLACEHOLDER_LOOP_REPEATS);
      });
    };
    expandLoop(EACH_RE);
    expandLoop(RANGE_RE);

    const expandIf = (re: RegExp, invert = false) => {
      out = out.replace(re, (_m, path: string, body: string) => {
        const [whenTrue, whenFalse] = splitOnElse(body);
        const chosen = (() => {
          if (!root && !scope) {
            // Nothing to decide with: show the main branch, which is what an
            // author is usually working on.
            return invert ? whenFalse : whenTrue;
          }
          const found = lookup(scope, path) ?? lookup(root, path);
          const truthy = isTruthy(found);
          return (invert ? !truthy : truthy) ? whenTrue : whenFalse;
        })();
        return render(chosen, scope, root, depth + 1);
      });
    };
    expandIf(IF_RE);
    expandIf(GO_IF_RE);
    expandIf(UNLESS_RE, true);

    if (out === before) break;
  }

  out = renderDates(out, scope, root);
  out = renderScalars(out, scope, root);
  return stripUnclosedHelpers(out);
};

/**
 * Removes block syntax that survived expansion.
 *
 * A template being written is frequently unbalanced — the closing tag has not
 * been typed yet — and the scalar pass cannot match `{{#if vip}}` because its
 * contents contain a space. Leaving it puts raw template syntax in front of the
 * reader, which is worse than showing the body without its condition.
 */
const stripUnclosedHelpers = (html: string): string =>
  html
    .replace(/\{\{\s*[#/^][^}]*\}\}/g, '')
    .replace(/\{\{\s*(?:else|end)\s*\}\}/g, '');

/** Renders a template with optional sample data. */
export const renderTemplatePreview = (html: string, data?: PreviewData): string => {
  if (!html) return '';
  return render(html, data, data);
};

/**
 * Collects the variable names a template refers to.
 *
 * Used to offer the author a starting point for sample data, so they are not
 * asked to guess what the template needs.
 */
export const collectVariables = (html: string): string[] => {
  const found = new Set<string>(collectDateVariables(html));
  const re = /\{\{\s*([#/^]?)\s*\.?([\w.]+)\s*\}\}/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(html)) !== null) {
    const [, prefix, name] = m;
    if (prefix) continue;
    if (HANDLEBARS_KEYWORDS.has(name)) continue;
    found.add(name);
  }
  return [...found].sort();
};

/**
 * The variables a template formats as dates.
 *
 * They need sample data of their own: "Sample created at" is not a date, so
 * offering it would make every suggested preview report a field that is not one.
 */
const collectDateVariables = (html: string): Set<string> => {
  const found = new Set<string>();
  for (const m of html.matchAll(DATE_ACTION_RE)) {
    const call = parseDateCall(m[1]);
    if (call) found.add(call.path);
  }
  return found;
};

/** Builds sample data covering every variable a template uses. */
export const suggestSampleData = (html: string): PreviewData => {
  const dates = collectDateVariables(html);
  const data: PreviewData = {};
  for (const name of collectVariables(html)) {
    // Only top-level names; a dotted path implies a shape this cannot guess.
    if (name.includes('.')) continue;
    data[name] = dates.has(name)
      ? new Date().toISOString().replace(/\.\d+Z$/, 'Z')
      : `Sample ${name.replace(/_/g, ' ')}`;
  }
  return data;
};
