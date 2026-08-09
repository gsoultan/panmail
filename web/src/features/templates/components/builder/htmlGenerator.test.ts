import { test, expect, describe } from 'bun:test';
import {
  generateHTML,
  styleToString,
  vmlArcsize,
  MSO_PREHEADER_MAX,
  MOBILE_BREAKPOINT,
  TABLET_BREAKPOINT,
  VIEWPORT_WIDTHS,
} from './htmlGenerator';
import type { Block, EmailDesign } from './types';

const design = (blocks: Block[], bodyStyle: EmailDesign['bodyStyle'] = {}): EmailDesign => ({
  blocks,
  bodyStyle,
});

const block = (type: Block['type'], content: any = {}, style: any = {}): Block => ({
  id: 'b1',
  type,
  content,
  style,
});

describe('escaping', () => {
  // An alt text with a quote used to close the attribute early, so everything
  // after it was parsed as markup rather than as the alt value.
  test('an attribute value cannot close its own attribute', () => {
    const html = generateHTML(
      design([block('image', { src: 'https://x.test/a.png', alt: 'a" onerror="alert(1)' })]),
    );
    expect(html).not.toContain('onerror="alert(1)"');
    expect(html).toContain('&quot; onerror=&quot;');
  });

  test('heading text is escaped', () => {
    const html = generateHTML(design([block('heading', { text: '<script>alert(1)</script>' })]));
    expect(html).not.toContain('<script>');
    expect(html).toContain('&lt;script&gt;');
  });

  test('a heading level cannot inject a tag name', () => {
    const html = generateHTML(
      design([block('heading', { text: 'hi', level: 'h1 onload=alert(1)' })]),
    );
    expect(html).toContain('<h1 style=');
    expect(html).not.toContain('onload=alert(1)');
  });

  test('button label and list items are escaped', () => {
    const btn = generateHTML(design([block('button', { label: '<b>x</b>', url: 'https://x.test' })]));
    expect(btn).toContain('&lt;b&gt;x&lt;/b&gt;');

    const list = generateHTML(design([block('list', { items: ['<img onerror=x>'] })]));
    expect(list).not.toContain('<img onerror');
    expect(list).toContain('&lt;img onerror=x&gt;');
  });

  test('table headers and cells are escaped', () => {
    const html = generateHTML(
      design([block('table', { headers: ['<b>H</b>'], rows: [['<i>c</i>']] })]),
    );
    expect(html).toContain('&lt;b&gt;H&lt;/b&gt;');
    expect(html).toContain('&lt;i&gt;c&lt;/i&gt;');
  });

  test('the preheader is escaped', () => {
    const html = generateHTML({ ...design([]), preheader: '</div><script>x</script>' });
    expect(html).not.toContain('<script>x</script>');
  });

  // The one intentional exception: this editor is labelled "Text (HTML)".
  test('the text block keeps author HTML', () => {
    const html = generateHTML(design([block('text', { text: '<p>hello <b>world</b></p>' })]));
    expect(html).toContain('<p>hello <b>world</b></p>');
  });

  // Merge tags must survive escaping or personalisation silently breaks.
  test('handlebars expressions pass through intact', () => {
    const html = generateHTML(
      design([block('heading', { text: 'Hi {{name}}' }), block('list', { items: ['{{item}}'] })]),
    );
    expect(html).toContain('Hi {{name}}');
    expect(html).toContain('<li>{{item}}</li>');
  });
});

describe('url safety', () => {
  test('javascript: links are neutralised', () => {
    for (const url of ['javascript:alert(1)', '  JavaScript:alert(1)', 'vbscript:x']) {
      const html = generateHTML(design([block('button', { label: 'go', url })]));
      expect(html.toLowerCase()).not.toContain('javascript:');
      expect(html.toLowerCase()).not.toContain('vbscript:');
      expect(html).toContain('href="#"');
    }
  });

  test('an image link cannot smuggle a script scheme', () => {
    const html = generateHTML(
      design([block('image', { src: 'https://x.test/a.png', linkUrl: 'javascript:alert(1)' })]),
    );
    expect(html.toLowerCase()).not.toContain('javascript:');
  });

  test('a social icon cannot be a javascript url', () => {
    const html = generateHTML(
      design([block('social', { links: [{ platform: 'X', url: 'javascript:x', icon: 'https://i.test/i.png' }] })]),
    );
    expect(html.toLowerCase()).not.toContain('javascript:');
  });

  test('ordinary and templated urls are preserved', () => {
    const html = generateHTML(
      design([block('button', { label: 'go', url: 'https://x.test/a?b=1&c=2' })]),
    );
    expect(html).toContain('https://x.test/a?b=1&amp;c=2');

    const tpl = generateHTML(design([block('button', { label: 'go', url: '{{unsubscribe_url}}' })]));
    expect(tpl).toContain('href="{{unsubscribe_url}}"');
  });
});

describe('css correctness', () => {
  // The spacer NumberInput stores a plain number, which used to reach the
  // stylesheet as `height: 20` and be discarded.
  test('numeric lengths get a unit', () => {
    expect(styleToString({ height: 20 })).toBe('height: 20px');
    expect(styleToString({ marginTop: 8 })).toBe('margin-top: 8px');
  });

  test('unitless properties keep their bare number', () => {
    expect(styleToString({ lineHeight: 1.5 })).toBe('line-height: 1.5');
    expect(styleToString({ fontWeight: 700 })).toBe('font-weight: 700');
  });

  test('empty and undefined declarations are dropped, not stringified', () => {
    expect(styleToString({ color: undefined, backgroundColor: null, padding: '' } as any)).toBe('');
    expect(styleToString({ color: '#fff', backgroundColor: undefined } as any)).toBe('color: #fff');
  });

  test('vendor prefixes get their leading dash', () => {
    expect(styleToString({ WebkitTextSizeAdjust: '100%' })).toBe('-webkit-text-size-adjust: 100%');
  });

  // contentWidth is a builder concept and was leaking into the body as the
  // bogus declaration `content-width: 600px`.
  test('contentWidth never reaches the stylesheet', () => {
    const html = generateHTML(design([], { contentWidth: '700px', backgroundColor: '#fff' }));
    expect(html).not.toContain('content-width');
    expect(html).toContain('max-width: 700px');
  });

  test('a percentage content width falls back to a usable pixel width', () => {
    const html = generateHTML(design([], { contentWidth: '100%' }));
    // Previously produced width="100", a 100px-wide email.
    expect(html).not.toContain('width="100"');
    expect(html).toContain('width="600"');
  });
});

describe('email client compatibility', () => {
  test('a spacer is a table cell, not a bare div', () => {
    const html = generateHTML(design([block('spacer', { height: 32 })]));
    expect(html).toContain('height="32"');
    expect(html).toContain('line-height: 32px');
    expect(html).toContain('&nbsp;');
  });

  test('video does not rely on positioning or flexbox', () => {
    const html = generateHTML(
      design([block('video', { url: 'https://v.test/1', thumbnail: 'https://v.test/t.png' })]),
    );
    expect(html).not.toContain('position: absolute');
    expect(html).not.toContain('display: flex');
    expect(html).not.toContain('transform:');
    expect(html).toContain('Watch video');
  });

  test('layout tables are marked presentational and data tables are not', () => {
    const html = generateHTML(design([block('table', { headers: ['A'], rows: [['1']] })]));
    expect(html).toContain('role="presentation"');
    expect(html).toContain('<thead>');
    // The data table itself must keep its semantics.
    expect(html).not.toMatch(/role="presentation"[^>]*>\s*<thead>/);
  });

  test('an empty header row is omitted entirely', () => {
    const html = generateHTML(design([block('table', { headers: [], rows: [['1']] })]));
    expect(html).not.toContain('<thead>');
  });

  test('the document declares a language and a colour scheme', () => {
    const html = generateHTML(design([]));
    expect(html).toContain('<html lang="en"');
    expect(html).toContain('name="color-scheme"');
    expect(html).toContain('<title>');
  });

  test('an image with no source produces nothing rather than a broken icon', () => {
    expect(generateHTML(design([block('image', { src: '' })]))).not.toContain('<img');
  });
});

describe('responsive: mobile, tablet, desktop', () => {
  test('breakpoints are fixed, not derived from the design width', () => {
    // A 500px design used to emit its only media query at 500px, so it never
    // reached mobile rules on a 560px-wide phone viewport.
    const narrow = generateHTML(design([], { contentWidth: '500px' }));
    const wide = generateHTML(design([], { contentWidth: '800px' }));
    for (const html of [narrow, wide]) {
      expect(html).toContain(`max-width: ${MOBILE_BREAKPOINT}px`);
      expect(html).toContain(`max-width: ${TABLET_BREAKPOINT}px`);
    }
  });

  test('the shell is fluid, so a phone never scrolls sideways', () => {
    const html = generateHTML(design([], { contentWidth: '600px' }));
    // A fixed width attribute pins the message wider than a 375px screen.
    expect(html).toContain('class="email-container"');
    expect(html).toMatch(/class="email-container"[^>]*style="[^"]*max-width: 600px/);
    expect(html).not.toMatch(/width="600" class="email-container"/);
  });

  test('outlook still gets a fixed width through the mso conditional', () => {
    const html = generateHTML(design([], { contentWidth: '640px' }));
    expect(html).toContain('<!--[if mso]>');
    expect(html).toContain('width="640"');
  });

  test('columns stack and lose their gutter on mobile', () => {
    const html = generateHTML(
      design([
        block('columns', {
          columns: [
            { id: 'c1', width: '50%', blocks: [block('heading', { text: 'A' })] },
            { id: 'c2', width: '50%', blocks: [block('heading', { text: 'B' })] },
          ],
        }),
      ]),
    );
    expect(html).toContain('class="stack-column"');
    expect(html).toContain('.stack-column { display: block !important');
    expect(html).toContain('.stack-column + .stack-column');
  });

  test('images are fluid but keep a width attribute for outlook', () => {
    const html = generateHTML(
      design([block('image', { src: 'https://x.test/a.png', alt: 'a' }, { width: '600px' })]),
    );
    expect(html).toContain('class="fluid"');
    expect(html).toContain('width="600"');
    expect(html).toContain('max-width: 600px');
    expect(html).toContain('img.fluid { width: 100% !important');
  });

  test('buttons go full width and stay thumb-sized on mobile', () => {
    const html = generateHTML(design([block('button', { label: 'Go', url: 'https://x.test' })]));
    expect(html).toContain('mobile-full-width');
    expect(html).toContain('min-height:45px');
    expect(html).toContain('.mobile-full-width { width: 100% !important');
  });

  test('body text is held at 16px on mobile so iOS does not auto-zoom', () => {
    const html = generateHTML(design([block('text', { text: '<p>hi</p>' })]));
    expect(html).toContain('class="content-padding body-text"');
    expect(html).toContain('font-size: 16px !important');
  });

  test('headings scale down on mobile', () => {
    const html = generateHTML(design([block('heading', { text: 'Hi' })]));
    expect(html).toContain('h1 { font-size: 24px !important');
  });

  test('padding tightens at tablet and again at phone', () => {
    const html = generateHTML(design([]));
    expect(html).toContain('.content-padding { padding-left: 24px !important');
    expect(html).toContain('.content-padding { padding: 24px 16px !important');
  });

  test('a wide data table scrolls instead of widening the message', () => {
    const html = generateHTML(design([block('table', { headers: ['A', 'B'], rows: [['1', '2']] })]));
    expect(html).toContain('class="data-table-wrap"');
    expect(html).toContain('.data-table-wrap { overflow-x: auto !important');
  });

  test('preview viewport widths line up with the breakpoints', () => {
    expect(VIEWPORT_WIDTHS.mobile).toBeLessThanOrEqual(MOBILE_BREAKPOINT);
    expect(VIEWPORT_WIDTHS.tablet).toBeLessThanOrEqual(TABLET_BREAKPOINT);
    expect(VIEWPORT_WIDTHS.desktop).toBeGreaterThan(TABLET_BREAKPOINT);
  });
});

describe('structure', () => {
  test('nested columns render their children', () => {
    const html = generateHTML(
      design([
        block('columns', {
          columns: [{ id: 'c1', width: '50%', blocks: [block('heading', { text: 'Inner' })] }],
        }),
      ]),
    );
    expect(html).toContain('Inner');
  });

  // A design read back from storage is not guaranteed to be acyclic; a cycle
  // must truncate rather than hang the tab.
  test('a cyclic design terminates', () => {
    const parent: any = block('columns', { columns: [{ id: 'c1', width: '100%', blocks: [] }] });
    parent.content.columns[0].blocks.push(parent);
    expect(() => generateHTML(design([parent]))).not.toThrow();
  });

  test('ifVariable wraps the block in a conditional', () => {
    const html = generateHTML(design([block('heading', { text: 'Hi', ifVariable: 'has_discount' })]));
    expect(html).toContain('{{#if has_discount}}');
    expect(html).toContain('{{/if}}');
  });

  test('a loop emits an each block around the row template', () => {
    const html = generateHTML(
      design([block('table', { headers: ['P'], rows: [['{{this.name}}']], loopVariable: 'products' })]),
    );
    expect(html).toContain('{{#each products}}');
    expect(html).toContain('{{this.name}}');
    expect(html).toContain('{{/each}}');
  });

  test('an empty design still produces a valid document', () => {
    const html = generateHTML(design([]));
    expect(html).toStartWith('<!DOCTYPE html>');
    expect(html).toContain('</html>');
  });
});

describe('outlook specifics', () => {
  // VML arcsize is a proportion of the shape's smaller dimension, not a length.
  // Doubling the pixel radius is only correct for a 50px-tall button.
  test('arcsize is a percentage of the button height', () => {
    expect(vmlArcsize(6, 45)).toBe(13);
    expect(vmlArcsize(25, 50)).toBe(50);
    expect(vmlArcsize(0, 45)).toBe(0);
  });

  test('arcsize never exceeds 100 percent', () => {
    expect(vmlArcsize(500, 45)).toBe(100);
  });

  test('a zero or negative height cannot produce a division by zero', () => {
    expect(vmlArcsize(6, 0)).toBe(0);
    expect(vmlArcsize(6, -10)).toBe(0);
  });

  test('the rendered button carries a computed arcsize', () => {
    const html = generateHTML(design([
      block('button', { label: 'Go', url: 'https://x.test' }, { borderRadius: '6px' }),
    ]));
    expect(html).toContain('arcsize="13%"');
  });

  // A preheader longer than the preview line is not hidden — it spills after
  // the subject and pushes out the words meant to be read.
  test('an over-long preheader is truncated with an ellipsis', () => {
    const long = 'x'.repeat(MSO_PREHEADER_MAX + 50);
    const html = generateHTML({ ...design([]), preheader: long });
    expect(html).toContain('…');
    expect(html).not.toContain('x'.repeat(MSO_PREHEADER_MAX + 1));
  });

  test('a short preheader is left alone', () => {
    const html = generateHTML({ ...design([]), preheader: 'Your order shipped' });
    expect(html).toContain('Your order shipped');
    expect(html).not.toContain('…');
  });
});

describe('outlook line spacing and image borders', () => {
  // Word's engine treats line-height as a minimum and grows it to suit the
  // font's metrics, so a design spaced carefully in every other client comes
  // out looser in Outlook. Declaring the mode is the only way to pin it.
  test('the stylesheet declares the exact line-height mode', () => {
    const html = generateHTML(design([]));
    expect(html).toContain('mso-line-height-rule: exactly');
  });

  test('a spacer pins its own line-height, because the gap IS a line-height', () => {
    const html = generateHTML(design([
      { id: '1', type: 'spacer', content: { height: 32 }, style: {} } as Block,
    ]));
    const spacer = html.slice(html.indexOf('height="32"'));
    expect(spacer.slice(0, 200)).toContain('mso-line-height-rule: exactly');
  });

  // The label is vertically centred by line-height, so Outlook growing it
  // pushes the text off centre inside the VML shape.
  test('a button pins the line-height centring its label', () => {
    const html = generateHTML(design([
      { id: '1', type: 'button', content: { text: 'Buy', url: 'https://example.com' }, style: {} } as Block,
    ]));
    expect(html).toContain('line-height:45px;mso-line-height-rule:exactly;');
  });

  // Outlook ignores the CSS `border: 0` in the stylesheet for a linked image
  // and draws the link colour around it; only the attribute stops that.
  test('images carry the border attribute, not just the CSS rule', () => {
    const html = generateHTML(design([
      { id: '1', type: 'image', content: { src: 'https://example.com/a.png', alt: 'A' }, style: {} } as Block,
    ]));
    expect(html).toMatch(/<img[^>]*border="0"/);
  });

  test('a linked image still carries it, which is the case that actually breaks', () => {
    const html = generateHTML(design([
      { id: '1', type: 'image', content: { src: 'https://example.com/a.png', alt: 'A', linkUrl: 'https://example.com' }, style: {} } as Block,
    ]));
    expect(html).toMatch(/<img[^>]*border="0"/);
  });
});

describe('repeating an arbitrary block', () => {
  // The gap this closes: looping existed only inside `list` and `table`, so a
  // line item could be a row of text and nothing else. A cart row is an image
  // beside a name beside a price — a columns block — and could not repeat.
  test('a columns block can repeat, which is what a cart row actually is', () => {
    const html = generateHTML(design([
      block('columns', {
        loopVariable: 'items',
        columns: [{ id: 'c1', width: '30%', blocks: [block('image', { src: 'https://e.com/p.png', alt: '{{name}}' })] },
                  { id: 'c2', width: '70%', blocks: [block('heading', { text: '{{name}} — {{price}}' })] }],
      }),
    ]));

    expect(html).toContain('{{#each items}}');
    expect(html).toContain('{{/each}}');
    // The children render inside the loop, not beside it.
    const loop = html.slice(html.indexOf('{{#each items}}'), html.indexOf('{{/each}}'));
    expect(loop).toContain('{{name}}');
    expect(loop).toContain('{{price}}');
  });

  test('any block type can repeat, not just a special one', () => {
    for (const type of ['heading', 'text', 'button', 'image'] as const) {
      const html = generateHTML(design([
        block(type, { loopVariable: 'rows', text: 'x', src: 'https://e.com/a.png', url: 'https://e.com' }),
      ]));
      expect(html).toContain('{{#each rows}}');
    }
  });

  // list and table build their own each internally; wrapping again would
  // iterate the iteration.
  test('a list is not wrapped twice', () => {
    const html = generateHTML(design([
      block('list', { loopVariable: 'items', items: ['{{this}}'] }),
    ]));
    expect(html.match(/\{\{#each items\}\}/g)).toHaveLength(1);
  });

  test('a table is not wrapped twice', () => {
    const html = generateHTML(design([
      block('table', { loopVariable: 'products', headers: ['P'], rows: [['{{this.name}}']] }),
    ]));
    expect(html.match(/\{\{#each products\}\}/g)).toHaveLength(1);
  });

  // An empty cart that renders a heading, a total of zero and a gap reads as a
  // broken email rather than an empty one — and the author never sees it,
  // because they always have test data.
  test('an empty state renders as the else branch', () => {
    const html = generateHTML(design([
      block('heading', { text: '{{name}}', loopVariable: 'items', emptyText: 'Your cart is empty.' }),
    ]));
    expect(html).toContain('{{else}}');
    expect(html).toContain('Your cart is empty.');
  });

  test('no empty text means no else branch, rather than an empty one', () => {
    const html = generateHTML(design([
      block('heading', { text: '{{name}}', loopVariable: 'items' }),
    ]));
    expect(html).not.toContain('{{else}}');
  });

  test('the empty text is escaped like everything else', () => {
    const html = generateHTML(design([
      block('heading', { text: 'x', loopVariable: 'items', emptyText: '<script>alert(1)</script>' }),
    ]));
    expect(html).not.toContain('<script>');
    expect(html).toContain('&lt;script&gt;');
  });

  test('a blank loop variable leaves the block alone', () => {
    const html = generateHTML(design([block('heading', { text: 'Hi', loopVariable: '   ' })]));
    expect(html).not.toContain('{{#each');
    expect(html).toContain('Hi');
  });

  // Both wrappers on one block: repeat the row, but only when there is a
  // section to show at all.
  test('repeat and conditional compose, with the conditional outermost', () => {
    const html = generateHTML(design([
      block('heading', { text: '{{name}}', loopVariable: 'items', ifVariable: 'has_items' }),
    ]));
    expect(html.indexOf('{{#if has_items}}')).toBeLessThan(html.indexOf('{{#each items}}'));
  });
});
