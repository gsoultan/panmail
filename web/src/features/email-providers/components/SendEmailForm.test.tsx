import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

/**
 * The form's seeding effect, against the two lists it depends on.
 *
 * The effect that copies `initialTemplateId` / `initialProviderId` into the
 * form listed `templates` as a dependency, and `templates` was
 * `data?.templates || []` — a fresh array on any render where the query had no
 * data yet. So on a page that renders the form with no initial ids at all, the
 * effect saw changed dependencies every render, wrote three fields, and
 * re-rendered itself until React aborted the tree. /test-delivery crashed on
 * load with "Maximum update depth exceeded".
 */

/** A promise plus the handle to settle it, so a test controls when data lands. */
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
};

const PROVIDERS = [
  { id: 'prov-1111-2222-3333', name: 'Postmark' },
  { id: 'prov-4444-5555-6666', name: 'Mailgun' },
];

// Reassigned per test so each one chooses when — and whether — its lists land.
let templatesResult: Promise<any> = new Promise(() => {});
let providersResult: Promise<any> = new Promise(() => {});

mock.module('../../templates/services/template', () => ({
  templateService: { listTemplates: () => templatesResult },
}));

// The SMTP tab reads live listener state. Stubbed here so this file does not
// depend on a backend — and null rather than undefined, which is what the real
// accessor returns and what react-query needs to tell "no listener" apart from
// a broken query function.
mock.module('../../../services/settings', () => ({
  settingsService: {
    getSmtpSubmission: async () => null,
    getSettings: async () => ({ baseUrl: 'https://mail.example.com' }),
  },
}));

mock.module('../services/emailProvider', () => ({
  emailProviderService: {
    listProviders: () => providersResult,
    sendEmail: async () => ({}),
  },
}));

const { SendEmailForm } = await import('./SendEmailForm');

const renderForm = async (props: Record<string, unknown> = {}) => {
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  let result!: ReturnType<typeof render>;
  await act(async () => {
    result = render(
      <QueryClientProvider client={queryClient}>
        <MantineProvider>
          <SendEmailForm onSubmit={() => {}} {...props} />
        </MantineProvider>
      </QueryClientProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  return result;
};

/** The input Mantine associated with a given field label. */
const inputFor = (container: HTMLElement, label: string): HTMLInputElement => {
  const found = Array.from(container.querySelectorAll('label')).find((element) =>
    element.textContent?.startsWith(label),
  );
  if (!found) throw new Error(`no field labelled "${label}"`);

  const id = found.getAttribute('for');
  const input = id ? (document.getElementById(id) as HTMLInputElement | null) : null;
  if (!input) throw new Error(`the field labelled "${label}" has no input`);
  return input;
};

describe('SendEmailForm', () => {
  test('renders while both lists are still loading', async () => {
    // Neither query ever settles, which is the state every mount passes
    // through and the one the old dependency array could not survive.
    templatesResult = new Promise(() => {});
    providersResult = new Promise(() => {});

    await renderForm();

    expect(screen.getByText('1. Sender & Recipients')).toBeTruthy();
  });

  test('keeps a provider the user picked when the template list arrives later', async () => {
    // The lists get a new identity whenever the query refetches. Re-seeding on
    // that identity rather than on the props would drop the selection.
    const templates = deferred<any>();
    templatesResult = templates.promise;
    providersResult = Promise.resolve({ providers: PROVIDERS });

    const { container } = await renderForm();

    const select = inputFor(container, 'Email Provider');
    await act(async () => {
      fireEvent.click(select);
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    await act(async () => {
      fireEvent.click(screen.getByText(`Mailgun (${PROVIDERS[1].id.substring(0, 8)}...)`));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    expect(inputFor(container, 'Email Provider').value).toContain('Mailgun');

    await act(async () => {
      templates.resolve({ templates: [] });
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    expect(inputFor(container, 'Email Provider').value).toContain('Mailgun');
  });

  // Three ways into the gateway, and they are genuinely different integrations
  // rather than three renderings of one. The SDK tab is the recommended path;
  // the other two are the same send without the dependency.
  test('offers the SDK alongside the raw HTTP and SMTP paths', async () => {
    templatesResult = Promise.resolve({ templates: [] });
    providersResult = Promise.resolve({ providers: PROVIDERS });

    await renderForm();

    for (const tab of ['Test Form', 'SDK', 'API Request', 'SMTP Request']) {
      expect(screen.getByRole('tab', { name: tab }), tab).toBeTruthy();
    }
  });

  test('the SDK tab shows the published client, not a hand-rolled request', async () => {
    templatesResult = Promise.resolve({ templates: [] });
    providersResult = Promise.resolve({ providers: PROVIDERS });

    await renderForm();

    const tab = screen.getByRole('tab', { name: 'SDK' });
    await act(async () => {
      fireEvent.click(tab);
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    // Scoped to this panel: Mantine keeps every panel mounted, so the whole
    // tree also holds the API tab's cURL — which names the endpoint on purpose.
    const panelId = tab.getAttribute('aria-controls');
    const panel = panelId ? document.getElementById(panelId) : null;
    if (!panel) throw new Error('the SDK tab controls no panel');

    // Go leads the SDK tab, so its snippet is the one on screen.
    const shown = panel.textContent ?? '';
    expect(shown).toContain('github.com/gsoultan/panmail-sdk');
    expect(shown).toContain('client.Send(context.Background()');
    // The client owns the endpoint; spelling it out here would mean the
    // snippet had stopped going through the client.
    expect(shown).not.toContain('/panmail.v1.EmailService/SendEmail');
  });
});
