/**
 * Registers a DOM for `bun test`.
 *
 * Only the sanitiser needs one — DOMPurify parses markup with a real DOM rather
 * than with regexes, which is exactly why it can be trusted. The HTML generator
 * and the history reducer are pure and unaffected.
 *
 * jsdom rather than happy-dom, deliberately. happy-dom's DOMParser is fine, but
 * something in its tree-walking makes DOMPurify drop `<table>` elements and
 * anchors carrying a relative href — neither of which happens in a browser or
 * under jsdom. Testing a security control against an environment that
 * misreports its behaviour is worse than not testing it, and jsdom is what
 * DOMPurify itself is tested against.
 */
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!DOCTYPE html><html><body></body></html>', { url: 'http://localhost' });
const jsdomWindow = dom.window as unknown as Record<string, unknown>;

// Copy the window's globals across without clobbering anything Bun already
// provides — the test runner's own globals must keep working.
for (const key of Object.getOwnPropertyNames(jsdomWindow)) {
  if (key in globalThis) continue;
  try {
    (globalThis as Record<string, unknown>)[key] = jsdomWindow[key];
  } catch {
    // Some window properties are getter-only; skipping them is harmless.
  }
}

// These two are what DOMPurify actually looks for, so they are set explicitly
// rather than left to the loop above.
(globalThis as Record<string, unknown>).window = jsdomWindow;
(globalThis as Record<string, unknown>).document = jsdomWindow.document;
