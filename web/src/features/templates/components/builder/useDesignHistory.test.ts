import { test, expect, describe } from 'bun:test';
import {
  historyReducer,
  initHistory,
  COALESCE_MS,
  MAX_HISTORY,
  type HistoryState,
} from './useDesignHistory';
import type { EmailDesign } from './types';

const design = (n: number): EmailDesign => ({
  blocks: [{ id: String(n), type: 'text', content: { text: `v${n}` }, style: {} }],
  bodyStyle: {},
});

const label = (s: HistoryState) => (s.present.blocks[0]?.content as any)?.text;

// Time is an input, so the tests drive it explicitly rather than sleeping.
const set = (s: HistoryState, n: number, now: number, coalesce = false) =>
  historyReducer(s, { type: 'set', updater: () => design(n), coalesce, now });

describe('undo and redo', () => {
  test('walk the stack in both directions', () => {
    let s = initHistory(design(0));
    expect(s.past).toHaveLength(0);

    s = set(s, 1, 1000);
    s = set(s, 2, 5000);
    expect(label(s)).toBe('v2');

    s = historyReducer(s, { type: 'undo' });
    expect(label(s)).toBe('v1');
    s = historyReducer(s, { type: 'undo' });
    expect(label(s)).toBe('v0');
    expect(s.past).toHaveLength(0);
    expect(s.future).toHaveLength(2);

    s = historyReducer(s, { type: 'redo' });
    expect(label(s)).toBe('v1');
    s = historyReducer(s, { type: 'redo' });
    expect(label(s)).toBe('v2');
    expect(s.future).toHaveLength(0);
  });

  test('undo at the start and redo at the end are no-ops', () => {
    const s = initHistory(design(0));
    expect(historyReducer(s, { type: 'undo' })).toBe(s);
    expect(historyReducer(s, { type: 'redo' })).toBe(s);
  });
});

describe('coalescing', () => {
  // Without this a typed sentence becomes one undo step per keystroke.
  test('edits inside the window collapse into a single step', () => {
    let s = initHistory(design(0));
    for (let i = 1; i <= 5; i++) {
      s = set(s, i, 1000 + i * 50, true);
    }
    expect(label(s)).toBe('v5');
    expect(s.past).toHaveLength(1);

    s = historyReducer(s, { type: 'undo' });
    expect(label(s)).toBe('v0');
  });

  test('an edit past the window starts a new step', () => {
    let s = initHistory(design(0));
    s = set(s, 1, 1000, true);
    s = set(s, 2, 1000 + COALESCE_MS + 1, true);
    expect(s.past).toHaveLength(2);

    s = historyReducer(s, { type: 'undo' });
    expect(label(s)).toBe('v1');
  });

  test('an uncoalesced edit is always its own step', () => {
    let s = initHistory(design(0));
    s = set(s, 1, 1000);
    s = set(s, 2, 1010);
    expect(s.past).toHaveLength(2);
  });

  // Coalescing into a state that was just restored would silently eat the undo.
  test('an edit right after undo is never merged into the restored state', () => {
    let s = initHistory(design(0));
    s = set(s, 1, 1000);
    s = historyReducer(s, { type: 'undo' });
    s = set(s, 2, 1010, true);
    s = historyReducer(s, { type: 'undo' });
    expect(label(s)).toBe('v0');
  });
});

describe('branching and bounds', () => {
  test('a new edit clears a redo branch the user walked away from', () => {
    let s = initHistory(design(0));
    s = set(s, 1, 1000);
    s = historyReducer(s, { type: 'undo' });
    expect(s.future).toHaveLength(1);

    s = set(s, 9, 2000);
    expect(s.future).toHaveLength(0);
    expect(label(s)).toBe('v9');
  });

  test('reset drops history so a newly loaded template cannot be undone into the old one', () => {
    let s = initHistory(design(0));
    s = set(s, 1, 1000);
    s = historyReducer(s, { type: 'reset', design: design(7) });
    expect(label(s)).toBe('v7');
    expect(s.past).toHaveLength(0);
    expect(s.future).toHaveLength(0);
  });

  test('an update returning the identical object records no step', () => {
    const s = initHistory(design(0));
    const same = historyReducer(s, { type: 'set', updater: (p) => p, now: 1000 });
    expect(same).toBe(s);
  });

  test('history is bounded and keeps the most recent entries', () => {
    let s = initHistory(design(0));
    for (let i = 1; i <= MAX_HISTORY + 70; i++) {
      s = set(s, i, i * 10_000);
    }
    expect(s.past).toHaveLength(MAX_HISTORY);
    // The oldest entries are the ones dropped.
    expect((s.past[s.past.length - 1].blocks[0].content as any).text)
      .toBe(`v${MAX_HISTORY + 70 - 1}`);
  });
});
