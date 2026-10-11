import { beforeEach, describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { SentCopyMode } from '../../../../api/panmail/v1/sent_copy_pb';

/**
 * The sent-copy section through the real component, with the service mocked.
 *
 * sentCopyRules.test.ts covers the payload on its own; this covers it being
 * wired to the form — which fields each mode shows, and that a password the
 * administrator never touched is not sent over the stored one.
 */

const saved: any[] = [];
const savedMailboxes: any[] = [];
let encryptionAvailable = true;
let stored: Record<string, unknown> = {};

mock.module('../../../../services/sentCopies', () => ({
  sentCopyService: {
    getSettings: async () => ({ settings: stored, encryptionAvailable }),
    updateSettings: async (settings: any) => {
      saved.push(settings);
      return settings;
    },
    listMailboxes: async () => [
      { address: 'alice@example.com', username: '', passwordSet: true },
    ],
    saveMailbox: async (mailbox: any) => {
      savedMailboxes.push(mailbox);
      return mailbox;
    },
    deleteMailbox: async () => {},
  },
}));

const { SentCopiesSettings } = await import('./SentCopiesSettings');

const settle = () =>
  act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });

const renderSection = async () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const result = render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>
        <SentCopiesSettings />
      </MantineProvider>
    </QueryClientProvider>,
  );
  await settle();
  return result;
};

const click = async (element: HTMLElement) => {
  await act(async () => {
    fireEvent.click(element);
  });
  await settle();
};

beforeEach(() => {
  saved.length = 0;
  savedMailboxes.length = 0;
  encryptionAvailable = true;
  stored = { mode: SentCopyMode.UNSPECIFIED };
});

describe('SentCopiesSettings', () => {
  test('None shows no server fields', async () => {
    await renderSection();
    expect((screen.getByLabelText('None') as HTMLInputElement).checked).toBe(true);
    expect(screen.queryByLabelText('Host')).toBeNull();
    expect(screen.queryByLabelText('Master username')).toBeNull();
  });

  test('switching mode shows the fields that mode needs', async () => {
    await renderSection();

    await click(screen.getByLabelText('Shared admin login'));
    expect(screen.getByLabelText('Host')).toBeTruthy();
    expect(screen.getByLabelText('Master username')).toBeTruthy();
    expect(screen.getByLabelText('Master password')).toBeTruthy();
    expect(screen.queryByText('alice@example.com')).toBeNull();

    await click(screen.getByLabelText('Per-user login'));
    expect(screen.getByLabelText('Host')).toBeTruthy();
    expect(screen.queryByLabelText('Master username')).toBeNull();
    expect(await screen.findByText('alice@example.com')).toBeTruthy();

    await click(screen.getByLabelText('Bcc to sender'));
    expect(screen.queryByLabelText('Host')).toBeNull();
  });

  test('the port placeholder follows the SSL switch', async () => {
    await renderSection();
    await click(screen.getByLabelText('Per-user login'));
    const port = screen.getByLabelText('Port') as HTMLInputElement;
    expect(port.placeholder).toBe('993');
    await click(screen.getByRole('switch'));
    expect((screen.getByLabelText('Port') as HTMLInputElement).placeholder).toBe('143');
  });

  test('a stored master password is kept when the field is left blank', async () => {
    stored = {
      mode: SentCopyMode.SHARED_LOGIN,
      imapHost: 'imap.example.com',
      imapUseSsl: true,
      masterUsername: 'admin',
      masterPasswordSet: true,
    };
    await renderSection();

    const password = screen.getByLabelText('Master password') as HTMLInputElement;
    expect(password.value).toBe('');
    expect(password.placeholder).toBe('Stored — leave blank to keep');

    await click(screen.getByText('Save sent-copy settings'));

    expect(saved).toHaveLength(1);
    expect(saved[0].mode).toBe(SentCopyMode.SHARED_LOGIN);
    expect(saved[0].imapHost).toBe('imap.example.com');
    expect('masterPassword' in saved[0]).toBe(false);
  });

  test('a new master password is required and then sent', async () => {
    await renderSection();
    await click(screen.getByLabelText('Shared admin login'));

    fireEvent.change(screen.getByLabelText('Host'), { target: { value: 'imap.example.com' } });
    fireEvent.change(screen.getByLabelText('Master username'), { target: { value: 'admin' } });
    await click(screen.getByText('Save sent-copy settings'));
    expect(saved).toHaveLength(0);

    fireEvent.change(screen.getByLabelText('Master password'), { target: { value: 'hunter2' } });
    await click(screen.getByText('Save sent-copy settings'));
    expect(saved).toHaveLength(1);
    expect(saved[0].masterPassword).toBe('hunter2');
  });

  test('without encryption the login modes are disabled and explained', async () => {
    encryptionAvailable = false;
    await renderSection();
    expect(screen.getByText(/no data key to encrypt passwords/)).toBeTruthy();
    expect((screen.getByLabelText('Shared admin login') as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByLabelText('Per-user login') as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByLabelText('Bcc to sender') as HTMLInputElement).disabled).toBe(false);
  });
});
