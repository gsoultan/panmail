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
