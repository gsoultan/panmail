import { beforeEach, describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

/**
 * The confirm-before-delete path through the real page.
 *
 * retentionFields.test.ts covers which changes count as destructive; this
 * covers them being wired to the save button, which is where it actually
 * matters. Saving triggers a retention pass immediately, so a dialog that
 * fails to block the mutation is not a missing dialog — it is data already
 * deleted by the time anyone notices.
 */

const saved: any[] = [];
// When set, getSettings rejects — the failed-load path.
let loadFails = false;
const settings = {
  baseUrl: 'https://mail.example.com',
  retryPattern: ['5m'],
  logRetentionDays: 14,
  messageRetentionDays: 0,
  outboxRetentionDays: 0,
  webhookRetentionDays: 7,
  appLogRetentionDays: 0,
  inboundRetentionDays: 90,
  archiveRetentionDays: 0,
};

mock.module('../services/settings', () => ({
  settingsService: {
    getSettings: async () => {
      if (loadFails) throw new Error('network down');
      return settings;
    },
    updateSettings: async (values: any) => {
      saved.push(values);
      return values;
    },
  },
}));

const { SettingsPage } = await import('./SettingsPage');

const renderPage = async () => {
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  const result = render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>
        <SettingsPage />
      </MantineProvider>
    </QueryClientProvider>,
  );
  // Let the settings query resolve and populate the form.
  await act(async () => {
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  return result;
};

const click = async (element: HTMLElement) => {
  await act(async () => {
    fireEvent.click(element);
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
};

const save = () => click(screen.getByText('Save Settings'));

/**
 * The day field under a given label.
 *
 * By label rather than by position: the page groups seven number inputs among
 * other fields, and a test that counts them silently starts editing a
 * different policy the moment anything is reordered — which for these fields
 * means a test that says it confirmed a deletion while exercising the one
 * policy that never needed confirming.
 */
const dayInput = (container: HTMLElement, label: string): HTMLInputElement => {
  const found = Array.from(container.querySelectorAll('label')).find((element) =>
    element.textContent?.startsWith(label),
  );
  if (!found) throw new Error(`no field labelled "${label}"`);

  const id = found.getAttribute('for');
  const input = id ? (document.getElementById(id) as HTMLInputElement | null) : null;
  if (!input) throw new Error(`the field labelled "${label}" has no input`);
  return input;
};

const setDays = async (container: HTMLElement, label: string, value: string) => {
  await act(async () => {
    fireEvent.change(dayInput(container, label), { target: { value } });
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
};

describe('SettingsPage retention', () => {
  beforeEach(() => {
    saved.length = 0;
    loadFails = false;
  });

  test('a failed load disables saving instead of posting placeholders', async () => {
    loadFails = true;
    await renderPage();

    // The form's initial values are an empty base URL and zero for every
    // retention. Posting those would blank the base URL one-click unsubscribe
    // depends on, and record all seven retentions as a deliberate "keep
    // forever" that the config migration cannot tell from a real choice.
    const saveButton = screen.getByText('Save Settings').closest('button');
    expect(saveButton?.hasAttribute('disabled')).toBe(true);
    expect(screen.getByText('Settings could not be loaded')).toBeTruthy();

    await save();
    expect(saved).toHaveLength(0);
  });

  test('saves without a dialog when nothing destructive changed', async () => {
    const { container } = await renderPage();

    await setDays(container, 'Webhook notifications', '14');
    await save();

    expect(saved).toHaveLength(1);
    expect(saved[0].webhookRetentionDays).toBe(14);
  });

  test('switching on message content retention asks first', async () => {
    const { container } = await renderPage();

    await setDays(container, 'Message content', '30');
    await save();

    // Nothing saved yet: this is the change that deletes every body and
    // attachment older than a month, and it runs on save.
    expect(saved).toHaveLength(0);
    // findBy rather than getBy: the dialog mounts through a Mantine
    // transition, so asserting on the same tick passes or fails on timing.
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('This deletes data now')).toBeTruthy();
    // Scoped to the dialog because the label also appears on the form behind
    // it, and the thing worth asserting is that the dialog names the policy
    // being changed rather than warning about deletion in the abstract.
    expect(within(dialog).getByText('Message content')).toBeTruthy();
    expect(within(dialog).getByText('Keep every body and attachment forever. → Deleted after 30 days.')).toBeTruthy();
  });

  test('cancelling leaves the data alone', async () => {
    const { container } = await renderPage();

    await setDays(container, 'Message content', '30');
    await save();
    await click(await screen.findByText('Cancel'));

    expect(saved).toHaveLength(0);
  });

  test('confirming saves exactly what was in the form', async () => {
    const { container } = await renderPage();

    await setDays(container, 'Message content', '30');
    await save();
    await click(await screen.findByText('Save and delete'));

    expect(saved).toHaveLength(1);
    expect(saved[0].messageRetentionDays).toBe(30);
    // Untouched fields go back as they were, not as zeroes.
    expect(saved[0].inboundRetentionDays).toBe(90);
    expect(saved[0].baseUrl).toBe('https://mail.example.com');
  });

  test('turning a destructive policy off is not treated as a deletion', async () => {
    const { container } = await renderPage();

    // 90 days of received mail becomes "keep forever". Nothing is at risk, so
    // confirming here would only train people to click through the dialog.
    await setDays(container, 'Received mail', '0');
    await save();

    expect(saved).toHaveLength(1);
    expect(saved[0].inboundRetentionDays).toBe(0);
  });
});
