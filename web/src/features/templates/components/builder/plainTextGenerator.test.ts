import { test, expect, describe } from 'bun:test';
import { generatePlainText } from './plainTextGenerator';
import type { Block, EmailDesign } from './types';

const design = (blocks: Block[], extra: Partial<EmailDesign> = {}): EmailDesign => ({
  blocks,
  bodyStyle: {},
  ...extra,
});

const block = (type: Block['type'], content: any = {}): Block => ({
  id: 'b1', type, content, style: {},
});

describe('the text part carries the message, not the markup', () => {
  test('tags are stripped and entities decoded', () => {
    const out = generatePlainText(design([
      block('text', { text: '<p>Hello <b>world</b> &amp; friends</p>' }),
    ]));
    expect(out).toBe('Hello world & friends');
    expect(out).not.toContain('<');
    expect(out).not.toContain('&amp;');
  });

  test('paragraphs do not run together', () => {
    const out = generatePlainText(design([
      block('text', { text: '<p>First.</p><p>Second.</p>' }),
    ]));
    expect(out).toContain('First.');
    expect(out).toContain('Second.');
    expect(out.indexOf('Second.')).toBeGreaterThan(out.indexOf('First.'));
    expect(out).not.toContain('First.Second.');
  });

  test('a heading keeps its structure', () => {
    const out = generatePlainText(design([block('heading', { text: 'Your receipt' })]));
    expect(out).toContain('Your receipt');
    expect(out).toContain('====');
  });
});

describe('destinations survive', () => {
  // A bare anchor label loses the destination once the markup is gone, and a
  // bare URL loses the reason to click it. Both are needed.
  test('a link keeps both its label and its href', () => {
    const out = generatePlainText(design([
      block('text', { text: '<p>See the <a href="https://x.test/report">quarterly report</a>.</p>' }),
    ]));
    expect(out).toContain('quarterly report');
    expect(out).toContain('https://x.test/report');
  });

  test('a button becomes label and url', () => {
    const out = generatePlainText(design([
      block('button', { label: 'Track parcel', url: 'https://x.test/track' }),
    ]));
    expect(out).toBe('Track parcel: https://x.test/track');
  });

  test('a placeholder href is not offered as a link', () => {
    const out = generatePlainText(design([block('button', { label: 'Click', url: '#' })]));
    expect(out).toBe('Click');
    expect(out).not.toContain('#');
  });

  test('social links are listed by platform', () => {
    const out = generatePlainText(design([
      block('social', { links: [
        { platform: 'Twitter', url: 'https://x.test/acme' },
        { platform: 'Skipped', url: '#' },
      ]}),
    ]));
    expect(out).toContain('Twitter: https://x.test/acme');
    expect(out).not.toContain('Skipped');
  });
});

describe('images', () => {
  // An image with no alt contributes nothing to the text part — which is the
  // same nothing a screen reader gets.
  test('alt text represents the image', () => {
    expect(generatePlainText(design([block('image', { src: 'https://x/a.png', alt: 'Our new store' })])))
      .toBe('[Our new store]');
  });

  test('an image with no alt and no link contributes nothing', () => {
    expect(generatePlainText(design([block('image', { src: 'https://x/a.png' })]))).toBe('');
  });

  test('a linked image keeps its destination', () => {
    const out = generatePlainText(design([
      block('image', { src: 'https://x/a.png', alt: 'Sale', linkUrl: 'https://x.test/sale' }),
    ]));
    expect(out).toContain('Sale');
    expect(out).toContain('https://x.test/sale');
  });
});

describe('structure', () => {
  test('list items become bullets', () => {
    const out = generatePlainText(design([block('list', { items: ['One', 'Two'] })]));
    expect(out).toContain('- One');
    expect(out).toContain('- Two');
  });

  test('a table keeps its rows readable', () => {
    const out = generatePlainText(design([
      block('table', { headers: ['Item', 'Qty'], rows: [['Widget', '2']] }),
    ]));
    expect(out).toContain('Item | Qty');
    expect(out).toContain('Widget | 2');
  });

  // Columns are a visual arrangement with no meaning in text; flattening in
  // reading order beats faking them with padding.
  test('columns flatten in reading order', () => {
    const out = generatePlainText(design([
      { id: 'c', type: 'columns', style: {}, content: { columns: [
        { id: 'c1', blocks: [block('heading', { text: 'Left' })] },
        { id: 'c2', blocks: [block('heading', { text: 'Right' })] },
      ]}} as Block,
    ]));
    expect(out.indexOf('Left')).toBeGreaterThanOrEqual(0);
    expect(out.indexOf('Right')).toBeGreaterThan(out.indexOf('Left'));
  });

  test('a spacer contributes nothing and a divider is a rule', () => {
    expect(generatePlainText(design([block('spacer', { height: 40 })]))).toBe('');
    expect(generatePlainText(design([block('divider')]))).toContain('---');
  });
});

describe('personalisation stays in step with the HTML part', () => {
  test('merge tags pass through', () => {
    const out = generatePlainText(design([
      block('heading', { text: 'Hi {{name}}' }),
      block('text', { text: '<p>Order {{order_id}}</p>' }),
    ]));
    expect(out).toContain('Hi {{name}}');
    expect(out).toContain('Order {{order_id}}');
  });

  // If a block is conditional in HTML and unconditional in text, the two halves
  // of the same message disagree.
  test('a conditional block stays conditional', () => {
    const out = generatePlainText(design([
      block('heading', { text: 'VIP', ifVariable: 'is_vip' }),
    ]));
    expect(out).toContain('{{#if is_vip}}');
    expect(out).toContain('{{/if}}');
  });

  test('a loop stays a loop', () => {
    const out = generatePlainText(design([
      block('list', { loopVariable: 'items', items: ['{{this.name}}'] }),
    ]));
    expect(out).toContain('{{#each items}}');
    expect(out).toContain('{{this.name}}');
    expect(out).toContain('{{/each}}');
  });
});

describe('shape of the output', () => {
  test('the preheader leads, since that is what it is for', () => {
    const out = generatePlainText(design([block('heading', { text: 'Body' })], {
      preheader: 'Your order shipped',
    }));
    expect(out.startsWith('Your order shipped')).toBe(true);
  });

  test('long prose is wrapped', () => {
    const out = generatePlainText(design([
      block('text', { text: `<p>${'word '.repeat(60)}</p>` }),
    ]));
    for (const line of out.split('\n')) {
      expect(line.length).toBeLessThanOrEqual(78);
    }
  });

  // Breaking a URL makes it unclickable, which is worse than a long line.
  test('a long URL is never broken', () => {
    const url = 'https://x.test/' + 'a'.repeat(120);
    const out = generatePlainText(design([block('button', { label: 'Go', url })]));
    expect(out).toContain(url);
  });

  test('an empty design produces an empty string, not whitespace', () => {
    expect(generatePlainText(design([]))).toBe('');
  });

  test('there are never three blank lines in a row', () => {
    const out = generatePlainText(design([
      block('text', { text: '<p>A</p>' }),
      block('spacer', { height: 40 }),
      block('spacer', { height: 40 }),
      block('text', { text: '<p>B</p>' }),
    ]));
    expect(out).not.toMatch(/\n{3,}/);
  });

  test('a cyclic design terminates', () => {
    const parent: any = block('columns', { columns: [{ id: 'c1', blocks: [] }] });
    parent.content.columns[0].blocks.push(parent);
    expect(() => generatePlainText(design([parent]))).not.toThrow();
  });
});
