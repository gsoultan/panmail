import { beforeEach, describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

/**
 * Replacing the key re-encrypts every stored credential, and a key nobody kept
 * leaves a backup that restores to unreadable passwords. So the change is only
 * offered once the key is well formed and the admin has said it is saved, and
 * never where the environment owns the key.
 */

let dataKey: any = { keyId: 'f29d0772', managedByEnvironment: false, retiredKeys: 0 };
const changed: string[] = [];

mock.module('../../../../services/settings', () => ({
  settingsService: {
    getDataKey: async () => dataKey,
    changeDataKey: async (key: string) => {
      changed.push(key);
      return { dataKey: { ...dataKey, keyId: 'a1b2c3d4' }, generatedKey: '', rotated: 3 };
    },
  },
}));

const { DataKeySettings } = await import('./DataKeySettings');

const renderPanel = async () => {
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>
        <DataKeySettings />
      </MantineProvider>
    </QueryClientProvider>,
  );
  await act(async () => {
    await Promise.resolve();
  });
};

const changeButton = () => screen.getByRole('button', { name: 'Change key' });

describe('DataKeySettings', () => {
  beforeEach(() => {
    dataKey = { keyId: 'f29d0772', managedByEnvironment: false, retiredKeys: 0 };
    changed.length = 0;
  });

  test('shows the key fingerprint, never the key', async () => {
    await renderPanel();
    expect(await screen.findByText('f29d0772')).toBeTruthy();
    expect(screen.getByText('Stored in the config file')).toBeTruthy();
  });

  test('a generated key is only sent once the admin has saved it', async () => {
    await renderPanel();
    fireEvent.click(await screen.findByRole('button', { name: 'Change encryption key' }));

    fireEvent.click(await screen.findByRole('button', { name: 'Generate a key' }));
    const input = screen.getByLabelText('New key') as HTMLInputElement;
    const generated = input.value;
    expect(generated).toMatch(/^[0-9a-f]{64}$/);
    expect(changeButton().hasAttribute('disabled')).toBe(true);

    fireEvent.click(screen.getByLabelText('I have saved this key somewhere safe'));
    expect(changeButton().hasAttribute('disabled')).toBe(false);

    await act(async () => {
      fireEvent.click(changeButton());
    });
    expect(changed).toEqual([generated]);
  });

  test('a malformed key cannot be submitted', async () => {
    await renderPanel();
    fireEvent.click(await screen.findByRole('button', { name: 'Change encryption key' }));

    fireEvent.change(await screen.findByLabelText('New key'), { target: { value: 'abc123' } });
    expect(screen.getByText('Must be exactly 64 hex characters (32 bytes)')).toBeTruthy();
    expect(changeButton().hasAttribute('disabled')).toBe(true);
  });

  test('a key set by the environment is not offered for change', async () => {
    dataKey = { keyId: 'f29d0772', managedByEnvironment: true, retiredKeys: 0 };
    await renderPanel();
    expect(await screen.findByText('Set by PANMAIL_SECRET_KEY')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Change encryption key' })).toBeNull();
  });
});
