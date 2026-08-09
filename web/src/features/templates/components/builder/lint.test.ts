import { test, expect, describe } from 'bun:test';
import { lintDesign, countBySeverity } from './lint';
import type { Block, EmailDesign } from './types';

const design = (blocks: Block[], extra: Partial<EmailDesign> = {}): EmailDesign => ({
  blocks,
  bodyStyle: {},
  preheader: 'A preheader so that check does not fire',
  ...extra,
});

const block = (type: Block['type'], content: any = {}): Block => ({
  id: 'b1', type, content, style: {},
});

const ids = (d: EmailDesign) => lintDesign(d).map((f) => f.id);
const has = (d: EmailDesign, prefix: string) => ids(d).some((i) => i.startsWith(prefix));

describe('problems the preview cannot show', () => {
  // Most clients block images by default, so alt text is what many readers
  // actually get — the preview always looks fine.
  test('an image with no alt text is flagged', () => {
    expect(has(design([block('image', { src: 'https://x/a.png' })]), 'image-no-alt')).toBe(true);
  });

  test('an image with alt text is not flagged', () => {
    expect(has(design([block('image', { src: 'https://x/a.png', alt: 'Our store' })]), 'image-no-alt')).toBe(false);
  });

  test('an image with no source is an error, not a warning', () => {
    const f = lintDesign(design([block('image', {})])).find((x) => x.id.startsWith('image-no-src'));
    expect(f?.severity).toBe('error');
  });

  test('a placeholder button link is caught', () => {
    for (const url of ['', '#', 'https://']) {
      expect(has(design([block('button', { label: 'Go', url })]), 'button-no-url')).toBe(true);
    }
    expect(has(design([block('button', { label: 'Go', url: 'https://x.test' })]), 'button-no-url')).toBe(false);
  });

  test('a button with no label is caught', () => {
    expect(has(design([block('button', { label: '', url: 'https://x.test' })]), 'button-no-label')).toBe(true);
  });
});

describe('deliverability signals', () => {
  // A message that is one big image is a long-standing spam signature.
  test('an image-only message is flagged', () => {
    expect(has(design([block('image', { src: 'https://x/a.png', alt: 'Sale' })]), 'image-heavy')).toBe(true);
  });

  test('an image with real copy alongside it is not', () => {
    const d = design([
      block('image', { src: 'https://x/a.png', alt: 'Sale' }),
      block('text', { text: `<p>${'Real body copy here. '.repeat(10)}</p>` }),
    ]);
    expect(has(d, 'image-heavy')).toBe(false);
  });

  test('a missing preheader is flagged', () => {
    const d = design([block('text', { text: '<p>hi</p>' })], { preheader: '' });
    expect(has(d, 'no-preheader')).toBe(true);
  });

  test('a preheader of only whitespace counts as missing', () => {
    const d = design([block('text', { text: '<p>hi</p>' })], { preheader: '   ' });
    expect(has(d, 'no-preheader')).toBe(true);
  });

  test('a visible unsubscribe link satisfies the check', () => {
    const withLink = design([block('text', { text: '<p><a href="{{unsubscribe_url}}">Unsubscribe</a></p>' })]);
    expect(has(withLink, 'no-visible-unsubscribe')).toBe(false);

    const withButton = design([block('button', { label: 'Unsubscribe', url: '{{unsubscribe_url}}' })]);
    expect(has(withButton, 'no-visible-unsubscribe')).toBe(false);

    expect(has(design([block('text', { text: '<p>hi</p>' })]), 'no-visible-unsubscribe')).toBe(true);
  });
});

describe('coverage of the tree', () => {
  // A problem inside a column is just as broken as one at the top level.
  test('blocks nested in columns are checked', () => {
    const d = design([
      { id: 'c', type: 'columns', style: {}, content: { columns: [
        { id: 'c1', blocks: [block('image', { src: 'https://x/a.png' })] },
      ]}} as Block,
    ]);
    expect(has(d, 'image-no-alt')).toBe(true);
  });

  test('a cyclic design terminates', () => {
    const parent: any = block('columns', { columns: [{ id: 'c1', blocks: [] }] });
    parent.content.columns[0].blocks.push(parent);
    expect(() => lintDesign(design([parent]))).not.toThrow();
  });

  test('an empty design says so and stops', () => {
    const findings = lintDesign(design([], { preheader: '' }));
    expect(findings).toHaveLength(1);
    expect(findings[0].id).toBe('empty-design');
  });
});

describe('reporting', () => {
  test('findings point at the block they are about', () => {
    const f = lintDesign(design([{ ...block('image', { src: 'https://x/a.png' }), id: 'target' }]))
      .find((x) => x.id.startsWith('image-no-alt'));
    expect(f?.blockId).toBe('target');
  });

  test('severities are counted', () => {
    const counts = countBySeverity(lintDesign(design([
      block('image', {}),                                  // error: no src
      block('button', { label: '', url: '' }),             // two errors
    ], { preheader: '' })));
    expect(counts.error).toBeGreaterThanOrEqual(3);
    expect(counts.warning).toBeGreaterThanOrEqual(1);
  });

  test('a clean design produces nothing to fix', () => {
    const d = design([
      block('heading', { text: 'Your order shipped' }),
      block('text', { text: `<p>${'Body copy. '.repeat(20)}<a href="{{unsubscribe_url}}">Unsubscribe</a></p>` }),
      block('image', { src: 'https://x/a.png', alt: 'Package' }),
      block('button', { label: 'Track', url: 'https://x.test/track' }),
    ]);
    expect(lintDesign(d)).toHaveLength(0);
  });
});
