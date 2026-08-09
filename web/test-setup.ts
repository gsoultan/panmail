/**
 * Registers a DOM for `bun test`.
 *
 * The sanitiser needs one because DOMPurify parses markup with a real DOM
 * rather than with regexes, which is exactly why it can be trusted. The
 * component tests need one because they render Mantine.
 *
 * jsdom rather than happy-dom, deliberately. happy-dom's DOMParser is fine, but
 * something in its tree-walking makes DOMPurify drop `<table>` elements and
 * anchors carrying a relative href — neither of which happens in a browser or
 * under jsdom. Testing a security control against an environment that
 * misreports its behaviour is worse than not testing it, and jsdom is what
 * DOMPurify itself is tested against.
 */
import { JSDOM } from 'jsdom';

// pretendToBeVisual supplies requestAnimationFrame and its cancel, which
// Mantine's Transition schedules against and clears on unmount.
const dom = new JSDOM('<!DOCTYPE html><html><body></body></html>', {
  url: 'http://localhost',
  pretendToBeVisual: true,
});
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

/**
 * jsdom implements neither of these, and Mantine uses both.
 *
 * A component test that renders a Select or a Tabs hits FloatingIndicator,
 * which constructs a ResizeObserver in an effect and throws without one. The
 * stubs only need to exist — nothing under test asserts on layout, and a fake
 * that reported plausible sizes would be more misleading than one that reports
 * none. matchMedia is the same story for Mantine's responsive props.
 */
const g = globalThis as Record<string, unknown>;

if (!g.ResizeObserver) {
  g.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

if (!g.matchMedia) {
  g.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  });
}

/**
 * jsdom has no FontFaceSet, and Mantine's floating components subscribe to it
 * so a popover can reposition once a webfont changes the text metrics. Without
 * the stub the subscription throws during render.
 */
const doc = jsdomWindow.document as Record<string, unknown>;
if (!doc.fonts) {
  doc.fonts = {
    ready: Promise.resolve(),
    status: 'loaded',
    check: () => true,
    load: () => Promise.resolve([]),
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  };
}

// react-dom checks this to decide whether to warn about updates outside act().
g.IS_REACT_ACT_ENVIRONMENT = true;
