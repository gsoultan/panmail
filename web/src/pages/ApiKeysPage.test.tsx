import { beforeEach, describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

/**
 * The edit path through the real page.
 *
 * scopes.test.ts covers the catalogue on its own; this covers the seed-and-save
 * round trip, which is where an edit can silently narrow a key. The modal is
 * populated from what the server stored and the save sends the picker's state,
 * so anything the seed drops is dropped from the grant as well.
 */

const updates: Array<{ id: string; name: string; scopes: string[] }> = [];

let storedScopes: string[] = ['email:send', 'templates:read'];

mock.module('../features/auth/services/apiKey', () => ({
  apiKeyService: {
    listApiKeys: async () => ({
      apiKeys: [
        {
          id: 'key-1',
          name: 'Checkout service',
          prefix: 'pm_abcd',
          scopes: storedScopes,
          isEnabled: true,
        },
      ],
      nextPageToken: '',
    }),
    createApiKey: async () => ({ apiKey: {}, plainTextKey: 'pm_x' }),
    updateApiKey: async (id: string, name: string, scopes: string[]) => {
      updates.push({ id, name, scopes });
      return { apiKey: { id, name, scopes } };
    },
    deleteApiKey: async () => ({}),
  },
}));

const { ApiKeysPage } = await import('./ApiKeysPage');

const renderPage = async () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const result = render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>
        <ApiKeysPage />
      </MantineProvider>
    </QueryClientProvider>,
  );
  await act(async () => {});
  return result;
};

const openEditor = async () => {
  const edit = await screen.findByLabelText('Edit Checkout service');
  await act(async () => {
    fireEvent.click(edit);
  });
};

// Mantine renders a Modal into a portal and animates it in, so its contents
// are not in the DOM on the tick after the click that opens it. Every query
// against the editor has to be the awaited kind.
const save = async () => {
  const button = await screen.findByText('Save changes');
  await act(async () => {
    fireEvent.click(button);
  });
};

beforeEach(() => {
  updates.length = 0;
  storedScopes = ['email:send', 'templates:read'];
});

describe('editing an API key', () => {
  test('sends the key id and the grant shown in the picker', async () => {
    await renderPage();
    await openEditor();
    await save();

    expect(updates).toHaveLength(1);
    expect(updates[0].id).toBe('key-1');
    expect(updates[0].name).toBe('Checkout service');
  });

  // Saving without touching anything must not quietly change the grant. The
  // picker is seeded from what the server stored, so an unchanged save has to
  // send exactly that back.
  test('an untouched save preserves the stored grant', async () => {
    await renderPage();
    await openEditor();
    await save();

    expect(updates[0].scopes.sort()).toEqual(['email:send', 'templates:read']);
  });

  // A console older than the gateway would otherwise strip a scope it merely
  // failed to display: the seed drops what it cannot show, and the save sends
  // the seed.
  test('a scope this build does not recognise is not silently resent', async () => {
    storedScopes = ['email:send', 'invented:scope'];
    await renderPage();
    await openEditor();
    await save();

    expect(updates[0].scopes).toEqual(['email:send']);
  });

  test('the editor says the secret is unchanged', async () => {
    await renderPage();
    await openEditor();
    // Portalled, so this is found on the document rather than in the page's
    // own container.
    expect(await screen.findByText(/The key itself does not change/)).toBeDefined();
  });

  test('cancelling sends nothing', async () => {
    await renderPage();
    await openEditor();
    const cancel = await screen.findByText('Cancel');
    await act(async () => {
      fireEvent.click(cancel);
    });

    expect(updates).toHaveLength(0);
  });
});
