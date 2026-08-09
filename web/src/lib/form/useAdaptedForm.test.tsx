import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, render, renderHook } from '@testing-library/react';
import { useAdaptedForm, type FieldErrors } from './useAdaptedForm';

/**
 * These exercise the adapter through the props it hands to inputs, not through
 * TanStack's own API. That is the contract that matters: the section components
 * spread `getInputProps` onto Mantine inputs and never see the form library, so
 * a regression here shows up as a field that silently stops saving.
 */

interface Values {
  name: string;
  type: number;
  smtp: { host: string; useSsl: boolean; dkim: { domain: string } };
}

const initialValues: Values = {
  name: '',
  type: 1,
  smtp: { host: '', useSsl: false, dkim: { domain: '' } },
};

const setup = (opts: { validate?: (v: Values) => FieldErrors; onSubmit?: (v: Values) => void } = {}) =>
  renderHook(() =>
    useAdaptedForm<Values>({
      initialValues,
      validate: opts.validate,
      onSubmit: opts.onSubmit ?? (() => {}),
    }),
  );

const change = (props: Record<string, any>, value: string) =>
  act(() => props.onChange({ currentTarget: { value } }));

describe('getInputProps', () => {
  test('reads a top-level value', () => {
    const { result } = setup();
    expect(result.current.getInputProps('name').value).toBe('');
  });

  // The provider form addresses everything below the protocol by dotted path.
  test('reads a nested value', () => {
    const { result } = setup();
    expect(result.current.getInputProps('smtp.dkim.domain').value).toBe('');
  });

  test('writing a nested path updates the values', () => {
    const { result } = setup();
    change(result.current.getInputProps('smtp.host'), 'smtp.example.com');
    expect(result.current.values.smtp.host).toBe('smtp.example.com');
  });

  test('writing one nested field leaves its siblings alone', () => {
    const { result } = setup();
    change(result.current.getInputProps('smtp.host'), 'smtp.example.com');
    change(result.current.getInputProps('smtp.dkim.domain'), 'example.com');

    expect(result.current.values.smtp.host).toBe('smtp.example.com');
    expect(result.current.values.smtp.dkim.domain).toBe('example.com');
  });

  // Mantine's TextInput passes an event; NumberInput and Select pass the value.
  test('accepts a bare value as well as an event', () => {
    const { result } = setup();
    act(() => result.current.getInputProps('name').onChange('Primary'));
    expect(result.current.values.name).toBe('Primary');
  });

  test('a missing path reads as empty rather than undefined', () => {
    const { result } = setup();
    // Mantine warns about switching an input from uncontrolled to controlled,
    // so an absent field still has to produce a defined value.
    expect(result.current.getInputProps('sendgrid.apiKey').value).toBe('');
  });

  test('checkbox mode exposes checked, not value', () => {
    const { result } = setup();
    const props = result.current.getInputProps('smtp.useSsl', { type: 'checkbox' });

    expect(props.checked).toBe(false);
    expect(props).not.toHaveProperty('value');

    act(() => (props.onChange as any)({ currentTarget: { checked: true } }));
    expect(result.current.values.smtp.useSsl).toBe(true);
  });
});

describe('validation', () => {
  const validate = (v: Values): FieldErrors => ({
    name: v.name.length < 2 ? 'Name must have at least 2 characters' : undefined,
  });

  // Showing "too short" on a form nobody has typed into yet is noise; Mantine
  // waited for a touch or a submit and so does this.
  test('an untouched field shows no error', () => {
    const { result } = setup({ validate });
    expect(result.current.getInputProps('name').error).toBeUndefined();
  });

  test('the error appears once the field is blurred', () => {
    const { result } = setup({ validate });
    act(() => (result.current.getInputProps('name').onBlur as any)());
    expect(result.current.getInputProps('name').error).toBe('Name must have at least 2 characters');
  });

  test('the error clears when the value becomes valid', () => {
    const { result } = setup({ validate });
    act(() => (result.current.getInputProps('name').onBlur as any)());
    change(result.current.getInputProps('name'), 'Primary');
    expect(result.current.getInputProps('name').error).toBeUndefined();
  });

  test('errors are reported for the whole form regardless of touch state', () => {
    const { result } = setup({ validate });
    expect(result.current.errors.name).toBe('Name must have at least 2 characters');
  });
});

describe('submission', () => {
  const validate = (v: Values): FieldErrors => ({
    name: v.name.length < 2 ? 'Name must have at least 2 characters' : undefined,
  });

  const submitForm = (form: ReturnType<typeof useAdaptedForm<Values>>) =>
    act(() => form.onSubmit()({ preventDefault: () => {} } as React.FormEvent));

  test('a valid form submits the current values', async () => {
    const onSubmit = mock(() => {});
    const { result } = setup({ validate, onSubmit });

    change(result.current.getInputProps('name'), 'Primary');
    change(result.current.getInputProps('smtp.host'), 'smtp.example.com');
    await submitForm(result.current);

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const values = (onSubmit.mock.calls[0] as unknown as [Values])[0];
    expect(values.name).toBe('Primary');
    expect(values.smtp.host).toBe('smtp.example.com');
  });

  test('an invalid form does not submit', async () => {
    const onSubmit = mock(() => {});
    const { result } = setup({ validate, onSubmit });

    await submitForm(result.current);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  test('a blocked submit reveals the errors', async () => {
    const { result } = setup({ validate });
    await submitForm(result.current);
    expect(result.current.getInputProps('name').error).toBe('Name must have at least 2 characters');
  });

  test('submitting does not reload the page', async () => {
    const preventDefault = mock(() => {});
    const { result } = setup({ validate });

    await act(() => result.current.onSubmit()({ preventDefault } as unknown as React.FormEvent));
    expect(preventDefault).toHaveBeenCalled();
  });

  test('an explicit handler receives the values instead', async () => {
    const onSubmit = mock(() => {});
    const handler = mock(() => {});
    const { result } = setup({ validate, onSubmit });

    change(result.current.getInputProps('name'), 'Primary');
    await act(() => result.current.onSubmit(handler)({ preventDefault: () => {} } as React.FormEvent));

    expect(handler).toHaveBeenCalledTimes(1);
    expect(onSubmit).not.toHaveBeenCalled();
  });
});

describe('rendering', () => {
  // The provider form swaps which fields exist based on `values.type`, so the
  // component has to re-render when a value changes. A field subscription that
  // did not propagate would leave the wrong protocol's fields on screen.
  test('a value change re-renders the consumer', () => {
    const Probe: React.FC = () => {
      const form = useAdaptedForm<Values>({ initialValues, onSubmit: () => {} });
      return (
        <div>
          <span data-testid="host">{form.values.smtp.host}</span>
          <button onClick={() => form.setFieldValue('smtp.host', 'changed.example.com')}>set</button>
        </div>
      );
    };

    const { getByTestId, getByText } = render(<Probe />);
    expect(getByTestId('host').textContent).toBe('');

    act(() => getByText('set').click());
    expect(getByTestId('host').textContent).toBe('changed.example.com');
  });
});
