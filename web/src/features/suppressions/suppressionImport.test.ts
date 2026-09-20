import { describe, expect, test } from 'bun:test';
import {
  MAX_ENTRIES_PER_REQUEST,
  chunkEntries,
  describeImport,
  parseSuppressionList,
  totalise,
} from './suppressionImport';

describe('parseSuppressionList', () => {
  test('one address per line', () => {
    const { entries } = parseSuppressionList('a@example.com\nb@example.com');
    expect(entries).toEqual([
      { email: 'a@example.com', reason: '' },
      { email: 'b@example.com', reason: '' },
    ]);
  });

  test('email and reason', () => {
    const { entries } = parseSuppressionList('a@example.com,hard bounce');
    expect(entries).toEqual([{ email: 'a@example.com', reason: 'hard bounce' }]);
  });

  // Files exported on Windows carry \r\n, and splitting on \n alone leaves a
  // trailing \r on every address — which then fails to parse server-side for
  // a reason nobody can see.
  test('handles CRLF and bare CR line endings', () => {
    expect(parseSuppressionList('a@example.com\r\nb@example.com').entries).toEqual([
      { email: 'a@example.com', reason: '' },
      { email: 'b@example.com', reason: '' },
    ]);
    expect(parseSuppressionList('a@example.com\rb@example.com').entries).toHaveLength(2);
  });

  test('blank lines are not entries', () => {
    const { entries, skipped } = parseSuppressionList('a@example.com\n\n   \nb@example.com\n');
    expect(entries).toHaveLength(2);
    expect(skipped).toEqual([]);
  });

  // A quoted field may contain the delimiter, which is how a reason survives
  // being a sentence.
  test('a quoted reason may contain commas', () => {
    const { entries } = parseSuppressionList('a@example.com,"bounced, mailbox full"');
    expect(entries[0].reason).toBe('bounced, mailbox full');
  });

  test('doubled quotes inside a quoted field are one quote', () => {
    const { entries } = parseSuppressionList('a@example.com,"said ""no"""');
    expect(entries[0].reason).toBe('said "no"');
  });

  test('a header row is not imported as an address', () => {
    expect(parseSuppressionList('email,reason\na@example.com,x').entries).toEqual([
      { email: 'a@example.com', reason: 'x' },
    ]);
    expect(parseSuppressionList('Email Address\na@example.com').entries).toHaveLength(1);
  });

  // Only the first line can be a header. A later row matching the pattern is
  // far more likely to be a real address at a domain like email.example.com.
  test('a later line that looks like a header is still an address', () => {
    const { entries } = parseSuppressionList('a@example.com\nemail@example.com');
    expect(entries).toHaveLength(2);
    expect(entries[1].email).toBe('email@example.com');
  });

  // Validation belongs to the server, which uses the same parser the send
  // path does. A second opinion here would only disagree with it.
  test('an unparseable address is passed through, not dropped', () => {
    const { entries } = parseSuppressionList('not an address');
    expect(entries).toEqual([{ email: 'not an address', reason: '' }]);
  });

  test('a line with a reason but no address is reported', () => {
    const { entries, skipped } = parseSuppressionList('a@example.com,ok\n,orphan reason');
    expect(entries).toHaveLength(1);
    expect(skipped).toEqual([2]);
  });

  test('an empty file yields nothing', () => {
    expect(parseSuppressionList('')).toEqual({ entries: [], skipped: [] });
  });
});

describe('chunkEntries', () => {
  test('a list within the limit is one request', () => {
    expect(chunkEntries([1, 2, 3], 10)).toEqual([[1, 2, 3]]);
  });

  test('a list over the limit is split', () => {
    const chunks = chunkEntries([1, 2, 3, 4, 5], 2);
    expect(chunks).toEqual([[1, 2], [3, 4], [5]]);
  });

  test('nothing to send is no requests', () => {
    expect(chunkEntries([])).toEqual([]);
  });

  // The default has to match MaxImportEntries in the usecase, or a full-size
  // chunk is refused with InvalidArgument.
  test('the default chunk is the server limit', () => {
    const entries = new Array(MAX_ENTRIES_PER_REQUEST + 1).fill(0);
    const chunks = chunkEntries(entries);
    expect(chunks).toHaveLength(2);
    expect(chunks[0]).toHaveLength(MAX_ENTRIES_PER_REQUEST);
  });
});

describe('totalise', () => {
  test('adds the per-chunk answers up', () => {
    const totals = totalise([
      { imported: 2, alreadySuppressed: 1, invalid: 0, invalidSamples: [] },
      { imported: 3, alreadySuppressed: 0, invalid: 1, invalidSamples: ['bad'] },
    ]);
    expect(totals).toEqual({
      imported: 5,
      alreadySuppressed: 1,
      invalid: 1,
      invalidSamples: ['bad'],
    });
  });

  // Each chunk caps its own samples, so keeping every chunk's would grow with
  // the file — which is the thing the cap exists to prevent.
  test('the collected samples stay bounded across chunks', () => {
    const chunk = {
      imported: 0,
      alreadySuppressed: 0,
      invalid: 20,
      invalidSamples: new Array(20).fill('bad'),
    };
    const totals = totalise([chunk, chunk, chunk]);
    expect(totals.invalid).toBe(60);
    expect(totals.invalidSamples).toHaveLength(20);
  });

  test('no chunks is all zeroes', () => {
    expect(totalise([])).toEqual({
      imported: 0,
      alreadySuppressed: 0,
      invalid: 0,
      invalidSamples: [],
    });
  });
});

describe('describeImport', () => {
  test('mentions only what happened', () => {
    expect(
      describeImport({ imported: 5, alreadySuppressed: 0, invalid: 0, invalidSamples: [] }),
    ).toBe('5 added');
  });

  test('accounts for the rest when there is a rest', () => {
    const summary = describeImport({
      imported: 5,
      alreadySuppressed: 2,
      invalid: 1,
      invalidSamples: [],
    });
    expect(summary).toContain('5 added');
    expect(summary).toContain('2 already on the list');
    expect(summary).toContain('1 unreadable');
  });

  // Importing the same file twice is a no-op, and the summary has to say so
  // rather than reading as a failure.
  test('a re-import reads as nothing to do, not as an error', () => {
    expect(
      describeImport({ imported: 0, alreadySuppressed: 9, invalid: 0, invalidSamples: [] }),
    ).toBe('0 added, 9 already on the list');
  });
});
