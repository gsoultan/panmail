import { test, expect, describe } from 'bun:test';
import { renderTemplatePreview, collectVariables, suggestSampleData } from './renderPreview';

describe('without sample data, the shape is still legible', () => {
  // An author writing a template needs to see the structure before the data
  // exists, so an unresolved variable is named rather than blanked.
  test('an unknown variable is shown by name', () => {
    expect(renderTemplatePreview('Hi {{name}}')).toBe('Hi [name]');
  });

  test('a loop repeats so the repeating region is visible', () => {
    const out = renderTemplatePreview('{{#each items}}<li>x</li>{{/each}}');
    expect(out).toBe('<li>x</li><li>x</li>');
  });

  test('a conditional shows the branch the author is working on', () => {
    expect(renderTemplatePreview('{{#if vip}}VIP{{/if}}')).toBe('VIP');
  });

  test('an empty template is empty, not undefined', () => {
    expect(renderTemplatePreview('')).toBe('');
  });
});

describe('with sample data, the preview is what a recipient sees', () => {
  // The whole reason for this: real values are not the length of their
  // placeholders, and length is what breaks a layout.
  test('variables are substituted', () => {
    expect(renderTemplatePreview('Hi {{name}}', { name: 'Alexandra' })).toBe('Hi Alexandra');
  });

  test('dotted paths resolve', () => {
    const out = renderTemplatePreview('{{order.customer.name}}', {
      order: { customer: { name: 'Bob' } },
    });
    expect(out).toBe('Bob');
  });

  test('a variable with no value is still named, not silently blank', () => {
    expect(renderTemplatePreview('Hi {{name}}', { other: 'x' })).toBe('Hi [name]');
  });

  test('values are escaped, so sample data cannot break the preview document', () => {
    const out = renderTemplatePreview('{{name}}', { name: '<script>alert(1)</script>' });
    expect(out).not.toContain('<script>');
    expect(out).toContain('&lt;script&gt;');
  });
});

describe('loops over real data', () => {
  test('iterate the supplied array', () => {
    const out = renderTemplatePreview('{{#each items}}<li>{{this}}</li>{{/each}}', {
      items: ['A', 'B', 'C'],
    });
    expect(out).toBe('<li>A</li><li>B</li><li>C</li>');
  });

  test('object items resolve their fields', () => {
    const out = renderTemplatePreview('{{#each items}}<td>{{name}}</td>{{/each}}', {
      items: [{ name: 'Widget' }, { name: 'Gadget' }],
    });
    expect(out).toBe('<td>Widget</td><td>Gadget</td>');
  });

  // Previewing the empty state is the point of supplying data at all.
  test('an empty array renders nothing', () => {
    expect(renderTemplatePreview('{{#each items}}<li>x</li>{{/each}}', { items: [] })).toBe('');
  });

  test('a loop can reach the root scope', () => {
    const out = renderTemplatePreview('{{#each items}}{{brand}}:{{name}} {{/each}}', {
      brand: 'Acme',
      items: [{ name: 'A' }, { name: 'B' }],
    });
    expect(out).toBe('Acme:A Acme:B ');
  });

  test('the Go range form behaves the same', () => {
    const out = renderTemplatePreview('{{range .items}}<li>{{this}}</li>{{end}}', { items: ['A'] });
    expect(out).toBe('<li>A</li>');
  });
});

describe('conditionals against real data', () => {
  test('a truthy value shows the branch', () => {
    expect(renderTemplatePreview('{{#if vip}}VIP{{/if}}', { vip: true })).toBe('VIP');
  });

  test('a falsy value hides it', () => {
    expect(renderTemplatePreview('{{#if vip}}VIP{{/if}}', { vip: false })).toBe('');
  });

  test('else is honoured', () => {
    const tpl = '{{#if vip}}VIP{{else}}Standard{{/if}}';
    expect(renderTemplatePreview(tpl, { vip: true })).toBe('VIP');
    expect(renderTemplatePreview(tpl, { vip: false })).toBe('Standard');
  });

  // Handlebars treats an empty array as falsy where JavaScript does not, and
  // the difference decides whether an empty section is shown or hidden.
  test('an empty array is falsy, as Handlebars has it', () => {
    expect(renderTemplatePreview('{{#if items}}Some{{else}}None{{/if}}', { items: [] })).toBe('None');
    expect(renderTemplatePreview('{{#if items}}Some{{else}}None{{/if}}', { items: [1] })).toBe('Some');
  });

  test('empty string and zero are falsy', () => {
    expect(renderTemplatePreview('{{#if n}}y{{else}}n{{/if}}', { n: 0 })).toBe('n');
    expect(renderTemplatePreview('{{#if s}}y{{else}}n{{/if}}', { s: '' })).toBe('n');
  });

  test('unless inverts', () => {
    expect(renderTemplatePreview('{{#unless vip}}Standard{{/unless}}', { vip: false })).toBe('Standard');
    expect(renderTemplatePreview('{{#unless vip}}Standard{{/unless}}', { vip: true })).toBe('');
  });
});

describe('nesting and robustness', () => {
  test('a conditional inside a loop', () => {
    const out = renderTemplatePreview(
      '{{#each items}}{{#if featured}}*{{/if}}{{name}} {{/each}}',
      { items: [{ name: 'A', featured: true }, { name: 'B', featured: false }] },
    );
    expect(out).toBe('*A B ');
  });

  test('a loop inside a conditional', () => {
    const out = renderTemplatePreview(
      '{{#if items}}{{#each items}}{{this}}{{/each}}{{else}}none{{/if}}',
      { items: ['x', 'y'] },
    );
    expect(out).toBe('xy');
  });

  // An unclosed block must not put raw template syntax in front of a reader.
  test('an unclosed block does not leak its tag', () => {
    const out = renderTemplatePreview('{{#if vip}}VIP', { vip: true });
    expect(out).not.toContain('{{');
    expect(out).toContain('VIP');
  });

  test('a template with no expressions is returned unchanged', () => {
    expect(renderTemplatePreview('<p>Plain</p>')).toBe('<p>Plain</p>');
  });
});

describe('helping the author supply data', () => {
  test('variables are collected, helpers are not', () => {
    const vars = collectVariables('{{#each items}}{{name}}{{/each}}{{#if vip}}{{email}}{{/if}}');
    expect(vars).toContain('name');
    expect(vars).toContain('email');
    expect(vars).not.toContain('each');
    expect(vars).not.toContain('if');
  });

  test('suggested data covers the top-level variables', () => {
    const data = suggestSampleData('Hi {{first_name}}, order {{order_id}}');
    expect(Object.keys(data).sort()).toEqual(['first_name', 'order_id']);
    expect(String(data.first_name)).toContain('first name');
  });

  test('suggested data leaves dotted paths alone, since their shape is unknowable', () => {
    expect(suggestSampleData('{{order.total}}')).toEqual({});
  });
});

describe('a loop with an empty branch', () => {
  // An empty cart must show the empty text, not the item template. Without
  // splitting on else the preview rendered both, which is the one combination
  // that can never be correct.
  test('an empty array takes the else branch', () => {
    const html = renderTemplatePreview(
      '{{#each items}}<li>{{name}}</li>{{else}}<p>Your cart is empty.</p>{{/each}}',
      { items: [] },
    );
    expect(html).toContain('Your cart is empty.');
    expect(html).not.toContain('<li>');
  });

  test('a populated array takes the item branch and not the empty text', () => {
    const html = renderTemplatePreview(
      '{{#each items}}<li>{{name}}</li>{{else}}<p>Your cart is empty.</p>{{/each}}',
      { items: [{ name: 'Widget' }, { name: 'Gadget' }] },
    );
    expect(html).toContain('Widget');
    expect(html).toContain('Gadget');
    expect(html).not.toContain('Your cart is empty.');
  });

  // While authoring there is no data, and showing both branches would
  // misrepresent every actual render.
  test('with no data the item branch is shown, repeated', () => {
    const html = renderTemplatePreview(
      '{{#each items}}<li>{{name}}</li>{{else}}<p>Your cart is empty.</p>{{/each}}',
    );
    expect(html).toContain('[name]');
    expect(html).not.toContain('Your cart is empty.');
  });

  test('a loop with no else still renders nothing when empty', () => {
    const html = renderTemplatePreview('{{#each items}}<li>{{name}}</li>{{/each}}', { items: [] });
    expect(html.trim()).toBe('');
  });
});

describe('dates render, rather than showing the author raw template syntax', () => {
  const data = { created_at: '2026-12-01T09:30:00Z' };

  test('a Go reference layout', () => {
    expect(renderTemplatePreview('{{ .created_at.Format "2006-01-02" }}', data)).toBe('2026-12-01');
  });

  // Go's layout is its reference date, so writing the shape out with any other
  // date renders nonsense there. The server reads it as an example, and a
  // preview that disagreed would show a date the recipient never gets.
  test('an example date', () => {
    expect(renderTemplatePreview('{{ .created_at.Format "2026-12-01" }}', data)).toBe('2026-12-01');
  });

  test('single quotes, as Handlebars allows', () => {
    expect(renderTemplatePreview("{{ created_at.Format '2026-12-01' }}", data)).toBe('2026-12-01');
  });

  test('the YYYY-MM-DD dialect', () => {
    expect(renderTemplatePreview('{{ created_at.Format "DD/MM/YYYY" }}', data)).toBe('01/12/2026');
    expect(renderTemplatePreview('{{ created_at.Format "MMM D, YYYY" }}', data)).toBe('Dec 1, 2026');
  });

  test('a named layout', () => {
    expect(renderTemplatePreview('{{ created_at.Format "long" }}', data)).toBe('December 1, 2026');
  });

  test('a date inside a loop resolves against the item', () => {
    const out = renderTemplatePreview('{{#each orders}}[{{ placed_at.Format "date" }}]{{/each}}', {
      orders: [{ placed_at: '2026-12-01T09:30:00Z' }, { placed_at: '2026-12-24T18:00:00Z' }],
    });
    expect(out).toBe('[2026-12-01][2026-12-24]');
  });

  // The server refuses to send this template, so a preview that merely showed
  // [created_at] — the way a missing variable looks — would hide a failed send.
  test('a field that is not a date says so', () => {
    expect(renderTemplatePreview('{{ created_at.Format "date" }}', { created_at: 'Ada' })).toBe(
      '[created_at is not a date]',
    );
  });

  test('a field with no value is named, as any other variable is', () => {
    expect(renderTemplatePreview('{{ created_at.Format "date" }}', { other: 'x' })).toBe(
      '[created_at]',
    );
  });

  test('a formatted variable is offered sample data that is actually a date', () => {
    const sample = suggestSampleData('{{ created_at.Format "date" }} {{name}}');
    expect(collectVariables('{{ created_at.Format "date" }}')).toEqual(['created_at']);
    expect(renderTemplatePreview('{{ created_at.Format "YYYY" }}', sample)).toMatch(/^\d{4}$/);
    expect(sample.name).toBe('Sample name');
  });
});

describe('a time zone is applied, since a recipient reads a wall clock', () => {
  // 09:30 UTC is 16:30 in Jakarta.
  const data = { StartAt: '2026-12-01T09:30:00Z' };

  test("Go's own longhand", () => {
    const out = renderTemplatePreview('{{ .StartAt.In (time.LoadLocation "Asia/Jakarta") }}', data);
    expect(out).toBe('2026-12-01 16:30:00 +0700 GMT+7');
  });

  test('the short spelling, chained with a layout', () => {
    const out = renderTemplatePreview('{{ (.StartAt.In "Asia/Jakarta").Format "2026-12-01 15:04" }}', data);
    expect(out).toBe('2026-12-01 16:30');
  });

  test('a zone as the second argument to Format', () => {
    const out = renderTemplatePreview('{{ StartAt.Format "DD MMM YYYY HH:mm" "Asia/Jakarta" }}', data);
    expect(out).toBe('01 Dec 2026 16:30');
  });

  // Changing the zone can change the day, and an invitation naming the wrong
  // day is worse than one naming the wrong hour.
  test('a zone change that moves the date', () => {
    const out = renderTemplatePreview('{{ (.StartAt.In "America/Los_Angeles").Format "DDD D MMM, HH:mm" }}', {
      StartAt: '2026-12-01T02:00:00Z',
    });
    expect(out).toBe('Mon 30 Nov, 18:00');
  });

  // A silent fall back to UTC would show an hour nobody chose.
  test('a zone that does not exist says so rather than showing UTC', () => {
    const out = renderTemplatePreview('{{ .StartAt.In "Asia/Jakata" }}', data);
    expect(out).toBe('[StartAt is not a date]');
  });

  test('a date method is still collected as a variable needing sample data', () => {
    expect(collectVariables('{{ .StartAt.In "Asia/Jakarta" }}')).toEqual(['StartAt']);
  });
});
