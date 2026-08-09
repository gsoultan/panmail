import { describe, expect, test } from 'bun:test';
import { countVariables, editDistance, findTypos } from './variableTypos';

const counts = (entries: Record<string, number>) => new Map(Object.entries(entries));

describe('editDistance', () => {
  test('identical names are zero apart', () => {
    expect(editDistance('name', 'name', 2)).toBe(0);
  });

  test('a transposition costs two, being a pair of substitutions', () => {
    expect(editDistance('frist_name', 'first_name', 2)).toBe(2);
  });

  test('a missing character costs one', () => {
    expect(editDistance('frst_name', 'first_name', 2)).toBe(1);
  });

  // The bound is what keeps this cheap enough to run on every keystroke.
  test('it gives up rather than computing a large distance exactly', () => {
    expect(editDistance('completely', 'different', 2)).toBeGreaterThan(2);
  });

  test('a large length difference is rejected without any work', () => {
    expect(editDistance('a', 'abcdefghij', 2)).toBeGreaterThan(2);
  });
});

describe('finding typos', () => {
  // The case worth catching: a name used throughout, and one place where it
  // was fumbled. The message goes out reading "Hi ," and nothing objects.
  test('a one-off close to an established name is flagged', () => {
    expect(findTypos(counts({ first_name: 5, frist_name: 1 })))
      .toEqual([{ variable: 'frist_name', suggestion: 'first_name' }]);
  });

  test('a name used consistently is not flagged', () => {
    expect(findTypos(counts({ first_name: 5, last_name: 4 }))).toEqual([]);
  });

  // Two names used once each give no basis for deciding which is wrong, and
  // guessing sends the author to correct the right one half the time.
  test('two one-offs are not judged against each other', () => {
    expect(findTypos(counts({ total: 1, totals: 1 }))).toEqual([]);
  });

  test('unrelated names are left alone', () => {
    expect(findTypos(counts({ first_name: 5, order_id: 3, unsubscribe_url: 1 }))).toEqual([]);
  });

  // On a short name one edit is as likely to be a different variable as a
  // slip, so flagging there would cry wolf constantly.
  test('short names are not second-guessed', () => {
    expect(findTypos(counts({ city: 4, cite: 1 }))).toEqual([]);
    expect(findTypos(counts({ id: 4, ad: 1 }))).toEqual([]);
  });

  test('a longer name earns more latitude', () => {
    expect(findTypos(counts({ verification_link: 4, verifcation_link: 1 })))
      .toEqual([{ variable: 'verifcation_link', suggestion: 'verification_link' }]);
  });

  // `order` and `order.id` are a real relationship, not a misspelling.
  test('a dotted path is not a typo of its own root', () => {
    expect(findTypos(counts({ order: 4, 'order.id': 1 }))).toEqual([]);
    expect(findTypos(counts({ 'order.id': 4, order: 1 }))).toEqual([]);
  });

  test('the nearest established name is the one suggested', () => {
    const found = findTypos(counts({ customer_name: 3, customer_email: 3, customer_nam: 1 }));
    expect(found).toEqual([{ variable: 'customer_nam', suggestion: 'customer_name' }]);
  });

  test('several typos are reported in a stable order', () => {
    const found = findTypos(counts({
      first_name: 4, frist_name: 1,
      company_name: 4, compnay_name: 1,
    }));
    expect(found.map((f) => f.variable)).toEqual(['compnay_name', 'frist_name']);
  });

  test('an empty design produces nothing', () => {
    expect(findTypos(new Map())).toEqual([]);
  });
});

describe('counting the variables a design uses', () => {
  test('counts repeats rather than collapsing them', () => {
    expect(countVariables({ a: '{{name}} {{name}}', b: '{{name}}' }).get('name')).toBe(3);
  });

  test('counts helper arguments and bare loop fields too', () => {
    const design = {
      blocks: [
        { content: { text: '{{#if vip}}Welcome back{{/if}}', loopVariable: 'items' } },
        { content: { text: '{{#each items}}x{{/each}}' } },
      ],
    };
    const found = countVariables(design);
    expect(found.get('vip')).toBe(1);
    expect(found.get('items')).toBe(2);
  });

  test('block helpers are not counted as variables', () => {
    const found = countVariables({ a: '{{#if vip}}{{else}}{{/if}}' });
    expect(found.has('if')).toBe(false);
    expect(found.has('else')).toBe(false);
  });

  // The end-to-end shape: a real design where one block fumbles the name.
  test('a design with a fumbled name yields exactly that suspicion', () => {
    const design = {
      preheader: 'Hi {{first_name}}',
      blocks: [
        { id: '1', content: { text: 'Hello {{first_name}}' } },
        { id: '2', content: { text: 'Thanks, {{first_name}}' } },
        { id: '3', content: { text: 'Bye {{frist_name}}' } },
      ],
    };
    expect(findTypos(countVariables(design)))
      .toEqual([{ variable: 'frist_name', suggestion: 'first_name' }]);
  });
});

// A design read back from storage is not guaranteed to be acyclic, and the
// scan runs on every keystroke in the builder — an unguarded recursion would
// take the tab down, not just the lint panel.
describe('a corrupt design', () => {
  test('a cycle terminates instead of exhausting the stack', () => {
    const parent: any = { id: '1', content: { text: '{{name}}', columns: [{ id: 'c', blocks: [] }] } };
    parent.content.columns[0].blocks.push(parent);

    expect(() => countVariables({ blocks: [parent] })).not.toThrow();
    expect(countVariables({ blocks: [parent] }).get('name')).toBe(1);
  });

  test('two blocks referencing each other terminate', () => {
    const a: any = { content: { text: '{{one}}' } };
    const b: any = { content: { text: '{{two}}', peer: a } };
    a.content.peer = b;

    expect(() => countVariables({ blocks: [a, b] })).not.toThrow();
  });
});
