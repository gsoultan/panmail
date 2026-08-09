import { describe, expect, test } from 'bun:test';
import {
  activeToken, filterVariables, insertVariable, variablesInDesign,
} from './variableSuggest';

/**
 * The caret arithmetic is the part that corrupts text when it is wrong, rather
 * than merely looking wrong, so it is pinned closely. `|` marks the caret in
 * these fixtures.
 */
const at = (withCaret: string) => {
  const caret = withCaret.indexOf('|');
  if (caret === -1) throw new Error('fixture needs a | for the caret');
  return { text: withCaret.replace('|', ''), caret };
};

const token = (withCaret: string) => {
  const { text, caret } = at(withCaret);
  return activeToken(text, caret);
};

describe('detecting the tag being typed', () => {
  test('an opened tag is active with an empty query', () => {
    expect(token('Hello {{|')).toEqual({ start: 6, query: '', end: 8 });
  });

  test('a partially typed name becomes the query', () => {
    expect(token('Hello {{na|')?.query).toBe('na');
  });

  test('a dotted path is a valid query', () => {
    expect(token('{{order.tot|')?.query).toBe('order.tot');
  });

  test('a closed tag before the caret is not active', () => {
    expect(token('Hello {{name}} |')).toBeNull();
  });

  test('a single brace is not a tag', () => {
    expect(token('Hello {na|')).toBeNull();
  });

  test('no braces at all is not a tag', () => {
    expect(token('Hello wor|')).toBeNull();
  });

  // Otherwise a second tag on the same line resolves against the first one's
  // braces and the insertion lands in the wrong place.
  test('the nearest opening braces win', () => {
    expect(token('{{a}} and {{b|')).toEqual({ start: 10, query: 'b', end: 13 });
  });

  // A stray `{{` left on an earlier line should not turn every word typed
  // afterwards into a suggestion prompt.
  test('a newline ends the search', () => {
    expect(token('{{oops\nHello wor|')).toBeNull();
  });

  test('a space means the braces were not a merge tag', () => {
    expect(token('{{not a var|')).toBeNull();
  });

  test('the caret inside an existing tag is active, so it can be replaced', () => {
    const t = token('{{na|me}}');
    expect(t?.query).toBe('na');
    // The end consumes nothing here: the caret is not directly before `}}`.
    expect(t?.end).toBe(4);
  });

  test('the caret just before the closing braces consumes them', () => {
    const t = token('{{name|}}');
    expect(t?.end).toBe(8);
  });
});

describe('filtering', () => {
  const vars = ['name', 'email', 'company_name', 'order_id'];

  test('an empty query offers everything', () => {
    expect(filterVariables(vars, '')).toEqual(vars);
  });

  test('a prefix match comes before a substring match', () => {
    // Someone typing "name" means `name`; `company_name` is the fallback.
    expect(filterVariables(vars, 'name')).toEqual(['name', 'company_name']);
  });

  test('matching ignores case', () => {
    expect(filterVariables(vars, 'ORDER')).toEqual(['order_id']);
  });

  test('no match offers nothing rather than everything', () => {
    expect(filterVariables(vars, 'zzz')).toEqual([]);
  });
});

describe('inserting', () => {
  test('replaces the partial tag and closes it', () => {
    const { text, caret } = at('Hello {{na|');
    const t = activeToken(text, caret)!;
    expect(insertVariable(text, t, 'name')).toEqual({ text: 'Hello {{name}}', caret: 14 });
  });

  test('the caret lands after the closing braces, ready to keep typing', () => {
    const { text, caret } = at('Hi {{|, welcome');
    const t = activeToken(text, caret)!;
    const out = insertVariable(text, t, 'name');
    expect(out.text).toBe('Hi {{name}}, welcome');
    expect(out.text.slice(out.caret)).toBe(', welcome');
  });

  // Retyping inside a finished tag must replace it, not nest a second one.
  test('replacing an existing tag does not nest braces', () => {
    const { text, caret } = at('{{name|}}');
    const t = activeToken(text, caret)!;
    expect(insertVariable(text, t, 'email').text).toBe('{{email}}');
  });

  test('text after the tag is preserved exactly', () => {
    const { text, caret } = at('a {{x| b {{y}} c');
    const t = activeToken(text, caret)!;
    expect(insertVariable(text, t, 'xx').text).toBe('a {{xx}} b {{y}} c');
  });
});

describe('collecting the variables a design already uses', () => {
  // A fixed built-in list meant the second use of a project's own variable was
  // typed from memory, which is how {{order_id}} and {{orderId}} end up in the
  // same template.
  test('finds tags anywhere in the design tree', () => {
    const design = {
      blocks: [
        { id: '1', type: 'heading', content: { text: 'Hi {{first_name}}' }, style: {} },
        {
          id: '2', type: 'columns', style: {},
          content: { columns: [{ id: 'c', blocks: [{ id: '3', type: 'text', content: { html: '{{order.total}}' }, style: {} }] }] },
        },
      ],
    };
    expect(variablesInDesign(design)).toEqual(['first_name', 'order.total']);
  });

  // These hold a bare name rather than a tag, so the pattern never sees them.
  test('finds loop and conditional variables, which are not written as tags', () => {
    const design = {
      blocks: [{ id: '1', type: 'heading', content: { text: 'x', loopVariable: 'items', ifVariable: 'has_discount' }, style: {} }],
    };
    expect(variablesInDesign(design)).toEqual(['has_discount', 'items']);
  });

  test('block helpers are not offered as variables', () => {
    expect(variablesInDesign({ a: '{{#if vip}}yes{{/if}} {{else}}' })).toEqual(['vip']);
  });

  test('each name is offered once however often it is used', () => {
    expect(variablesInDesign({ a: '{{name}}', b: '{{name}}', c: '{{name}}' })).toEqual(['name']);
  });

  test('the preheader and other loose strings count too', () => {
    expect(variablesInDesign({ preheader: 'Order {{order_id}} shipped', blocks: [] })).toEqual(['order_id']);
  });

  test('an empty design yields nothing rather than throwing', () => {
    expect(variablesInDesign({ blocks: [] })).toEqual([]);
    expect(variablesInDesign(null)).toEqual([]);
  });
});

describe('a corrupt design', () => {
  // Same guard as countVariables, and this one runs on every render of the
  // builder rather than only when the lint panel is open.
  test('a cycle terminates instead of exhausting the stack', () => {
    const parent: any = { id: '1', content: { text: '{{name}}', columns: [{ id: 'c', blocks: [] }] } };
    parent.content.columns[0].blocks.push(parent);

    expect(() => variablesInDesign({ blocks: [parent] })).not.toThrow();
    expect(variablesInDesign({ blocks: [parent] })).toEqual(['name']);
  });
});
