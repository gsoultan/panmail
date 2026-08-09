import { describe, expect, test } from 'bun:test';
import React, { useState } from 'react';
import { act, fireEvent, render } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { VariableInput } from './VariableInput';

/**
 * Driven through the DOM rather than the pure helpers, because the thing that
 * makes this usable is the wiring — the list opening on the right keystroke and
 * the caret landing somewhere sensible afterwards. variableSuggest.test.ts
 * covers the arithmetic underneath.
 */

const VARIABLES = ['name', 'email', 'order_id', 'company_name'];

const Harness: React.FC<{ initial?: string }> = ({ initial = '' }) => {
  const [value, setValue] = useState(initial);
  return (
    <MantineProvider>
      <VariableInput label="Field" value={value} onChange={setValue} variables={VARIABLES} />
      <output data-testid="value">{value}</output>
    </MantineProvider>
  );
};

const setup = async (initial = '') => {
  const view = render(<Harness initial={initial} />);
  await act(async () => {});
  const input = view.container.querySelector('input') as HTMLInputElement;
  return { ...view, input };
};

/** Types into the field the way a user would, keeping the caret at the end. */
const type = async (input: HTMLInputElement, text: string) => {
  await act(async () => {
    fireEvent.change(input, { target: { value: text } });
  });
  // jsdom drops the selection when the value is assigned, so the caret has to
  // be put back and announced — the component reads it to decide whether the
  // caret is inside a tag.
  await act(async () => {
    input.setSelectionRange(text.length, text.length);
    fireEvent.keyUp(input, { key: 'a' });
  });
  // Mantine animates the list in and out, so both opening and closing land a
  // frame later than the keystroke that caused them.
  await flush();
};

const flush = async () => {
  await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};

const suggestions = (body: HTMLElement) =>
  [...body.querySelectorAll('code')].map((c) => c.textContent);

describe('opening the list', () => {
  test('typing the opening braces offers every variable', async () => {
    const { input, baseElement } = await setup();
    await type(input, 'Hi {{');
    expect(suggestions(baseElement)).toEqual(['{{name}}', '{{email}}', '{{order_id}}', '{{company_name}}']);
  });

  test('typing a name narrows the list', async () => {
    const { input, baseElement } = await setup();
    await type(input, 'Hi {{na');
    expect(suggestions(baseElement)).toEqual(['{{name}}', '{{company_name}}']);
  });

  test('plain text offers nothing', async () => {
    const { input, baseElement } = await setup();
    await type(input, 'Hello there');
    expect(suggestions(baseElement)).toEqual([]);
  });

  test('a closed tag closes the list', async () => {
    const { input, baseElement } = await setup();
    await type(input, 'Hi {{name}} ');
    expect(suggestions(baseElement)).toEqual([]);
  });

  test('a name that matches nothing offers nothing rather than everything', async () => {
    const { input, baseElement } = await setup();
    await type(input, '{{zzz');
    expect(suggestions(baseElement)).toEqual([]);
  });
});

describe('accepting a suggestion', () => {
  test('Enter inserts the highlighted variable, closed', async () => {
    const { input, getByTestId } = await setup();
    await type(input, 'Hi {{na');
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }); });

    expect(getByTestId('value').textContent).toBe('Hi {{name}}');
  });

  test('the arrow keys move the selection before Enter takes it', async () => {
    const { input, getByTestId } = await setup();
    await type(input, 'Hi {{na');
    await act(async () => { fireEvent.keyDown(input, { key: 'ArrowDown' }); });
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }); });

    expect(getByTestId('value').textContent).toBe('Hi {{company_name}}');
  });

  test('the selection wraps rather than sticking at the end', async () => {
    const { input, getByTestId } = await setup();
    await type(input, 'Hi {{na');
    // Two matches, so two downs wrap back around to the first.
    for (const _ of [0, 1]) {
      await act(async () => { fireEvent.keyDown(input, { key: 'ArrowDown' }); });
    }
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }); });

    expect(getByTestId('value').textContent).toBe('Hi {{name}}');
  });

  test('Tab accepts too, since it is what completion usually means', async () => {
    const { input, getByTestId } = await setup();
    await type(input, '{{ord');
    await act(async () => { fireEvent.keyDown(input, { key: 'Tab' }); });

    expect(getByTestId('value').textContent).toBe('{{order_id}}');
  });

  test('clicking a suggestion inserts it', async () => {
    const { input, baseElement, getByTestId } = await setup();
    await type(input, '{{em');
    const option = [...baseElement.querySelectorAll('code')].find((c) => c.textContent === '{{email}}');
    // mousedown, because click fires after blur and the caret is gone by then.
    await act(async () => { fireEvent.mouseDown(option!.parentElement!); });

    expect(getByTestId('value').textContent).toBe('{{email}}');
  });

  test('text after the tag survives the insertion', async () => {
    const { input, getByTestId } = await setup();
    // Caret placed mid-string rather than at the end.
    await act(async () => {
      fireEvent.change(input, { target: { value: 'Hi {{na, welcome', selectionStart: 7 } });
    });
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }); });

    expect(getByTestId('value').textContent).toBe('Hi {{name}}, welcome');
  });
});

describe('dismissing', () => {
  test('Escape closes the list', async () => {
    const { input, baseElement } = await setup();
    await type(input, 'Hi {{na');
    await act(async () => { fireEvent.keyDown(input, { key: 'Escape' }); });
    await flush();

    expect(suggestions(baseElement)).toEqual([]);
  });

  // Otherwise Escape is useless: the list reappears on the next character and
  // Enter starts inserting variables the author was trying to avoid.
  test('the list stays closed while the same tag is being typed', async () => {
    const { input, baseElement } = await setup();
    await type(input, 'Hi {{na');
    await act(async () => { fireEvent.keyDown(input, { key: 'Escape' }); });
    await flush();
    await type(input, 'Hi {{nam');

    expect(suggestions(baseElement)).toEqual([]);
  });

  test('a new tag opens the list again', async () => {
    const { input, baseElement } = await setup();
    await type(input, 'Hi {{na');
    await act(async () => { fireEvent.keyDown(input, { key: 'Escape' }); });
    await type(input, 'Hi {{name}} and {{em');

    expect(suggestions(baseElement)).toEqual(['{{email}}']);
  });
});

describe('the field still behaves like a field', () => {
  test('Enter with no list open is left alone', async () => {
    const { input, getByTestId } = await setup();
    await type(input, 'Hello');
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }); });

    expect(getByTestId('value').textContent).toBe('Hello');
  });

  test('ordinary typing is not swallowed', async () => {
    const { input, getByTestId } = await setup();
    await type(input, 'Plain text, no tags');
    expect(getByTestId('value').textContent).toBe('Plain text, no tags');
  });
});
