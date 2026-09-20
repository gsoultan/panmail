import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, render, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';

let baseUrl = 'https://mail.example.com';

mock.module('../../../services/settings', () => ({
  settingsService: { getSettings: async () => ({ baseUrl }) },
}));

const { InboundWebhookSection } = await import('./InboundWebhookSection');
const { useAuthStore } = await import('../../../store/authStore');

/**
 * Mantine injects a <style> block into the container, so its textContent is
 * never empty even when the component under test renders null. Presence is
 * therefore measured by the section's own heading.
 */
const HEADING = 'Delivery events';

const renderSection = async (props: Partial<Parameters<typeof InboundWebhookSection>[0]> = {}) => {
  useAuthStore.setState({ selectedTenantID: 'tenant-1' });

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  const result = render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>
        <InboundWebhookSection
          providerType={ProviderType.SENDGRID}
          providerId="prov-1"
          value=""
          onChange={mock(() => {})}
          {...props}
        />
      </MantineProvider>
    </QueryClientProvider>,
  );
  // The base URL arrives from a query, so the URL is not in the DOM on the
  // first tick.
  await act(async () => {});
  return result;
};

describe('InboundWebhookSection', () => {
  test('shows the URL a provider should post to', async () => {
    const { container } = await renderSection();
    // The base URL arrives from a query, so this is the one assertion that has
    // to wait for it rather than reading the first render.
    await waitFor(() =>
      expect(container.textContent).toContain(
        'https://mail.example.com/webhooks/tenant-1/prov-1/sendgrid',
      ),
    );
  });

  // A transport provider has nobody to post events back, so the whole section
  // would be an invitation to configure something that can never fire.
  test('renders nothing at all for an SMTP provider', async () => {
    const { container } = await renderSection({ providerType: ProviderType.SMTP });
    expect(container.textContent).not.toContain(HEADING);
  });

  // The URL contains the provider's id, so on the create form there is no URL
  // to give yet. Saying so beats rendering a broken one.
  test('says to save first when the provider has no id', async () => {
    const { container } = await renderSection({ providerId: undefined });
    await waitFor(() => expect(container.textContent).toContain('Save the provider'));
    expect(container.textContent).not.toContain('/webhooks/');
  });

  // The dashboard is often on a private host; a provider has to reach this
  // from the internet, so an unset base URL is a real misconfiguration.
  test('points at the settings page when no base URL is configured', async () => {
    baseUrl = '';
    try {
      const { container } = await renderSection();
      await waitFor(() => expect(container.textContent).toContain(HEADING));
      expect(container.textContent).toContain('base URL');
      expect(container.textContent).not.toContain('/webhooks/');
    } finally {
      baseUrl = 'https://mail.example.com';
    }
  });

  test('asks each provider for the secret it actually uses', async () => {
    const postmark = await renderSection({ providerType: ProviderType.POSTMARK });
    expect(postmark.container.textContent).toContain('basic credentials');

    const ses = await renderSection({ providerType: ProviderType.SES });
    expect(ses.container.textContent).toContain('topic ARN');
  });

  // Reads redact the stored value, so the field is blank on every open. An
  // operator who is not told that will assume no secret is set.
  test('explains that a blank field keeps the stored secret', async () => {
    const { container } = await renderSection({ providerId: 'prov-1' });
    expect(container.textContent).toContain('Leaving it blank keeps what is already stored');
  });

  test('does not claim a stored value exists while creating', async () => {
    const { container } = await renderSection({ providerId: undefined });
    expect(container.textContent).not.toContain('Leaving it blank keeps what is already stored');
  });
});
