import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { fireEvent, render } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { RetentionSettings } from './RetentionSettings';
import { RETENTION_FIELDS, retentionValues, type RetentionKey } from './retentionFields';

const allForever = retentionValues(undefined);

const renderSettings = (
  values: Record<RetentionKey, number>,
  onChange: (key: RetentionKey, value: number) => void = () => {},
) =>
  render(
    <MantineProvider>
      <RetentionSettings values={values} onChange={onChange} />
    </MantineProvider>,
  );

describe('RetentionSettings', () => {
  test('every class panmail stores has a knob on the page', () => {
    const { getByText } = renderSettings(allForever);

    for (const field of RETENTION_FIELDS) {
      expect(getByText(field.label)).toBeTruthy();
    }
  });

  test('zero is labelled as keep-forever rather than shown as an empty field', () => {
    const { getAllByText } = renderSettings(allForever);

    // Seven policies, all off: an operator has to be able to see that at a
    // glance, because "0" on its own reads like a misconfiguration.
    expect(getAllByText('Kept forever')).toHaveLength(RETENTION_FIELDS.length);
  });

  test('a policy that deletes content says so once it is switched on', () => {
    const { getAllByText, queryAllByText } = renderSettings({
      ...allForever,
      messageRetentionDays: 30,
      logRetentionDays: 30,
    });

    // Only the destructive one is flagged. Delivery events are archived on
    // their way out, so flagging them too would make the warning meaningless.
    expect(getAllByText('Deletes content')).toHaveLength(1);
    expect(queryAllByText('Kept forever')).toHaveLength(RETENTION_FIELDS.length - 2);
  });

  test('editing a field reports the new value for that field', () => {
    const onChange = mock(() => {});
    const { container } = renderSettings(allForever, onChange);

    const messageInput = container.querySelectorAll('input')[1];
    fireEvent.change(messageInput, { target: { value: '30' } });

    expect(onChange).toHaveBeenCalledWith('messageRetentionDays', 30);
  });

  test('clearing a field means keep forever, not an invalid number', () => {
    const onChange = mock(() => {});
    const { container } = renderSettings({ ...allForever, messageRetentionDays: 30 }, onChange);

    const messageInput = container.querySelectorAll('input')[1];
    fireEvent.change(messageInput, { target: { value: '' } });

    // NaN here would post a field the server has to reject, and the operator
    // would see a save fail with nothing to correct.
    expect(onChange).toHaveBeenCalledWith('messageRetentionDays', 0);
  });
});
