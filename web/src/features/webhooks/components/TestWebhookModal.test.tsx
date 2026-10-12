import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { fireEvent, render, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { create } from '@bufbuild/protobuf';
import { WebhookSchema, WebhookTriggerEvent } from '../../../api/panmail/v1/webhook_pb';

const testWebhook = mock(async (_id: string, event?: WebhookTriggerEvent) => ({
  success: false,
  statusCode: 401,
  error: 'endpoint returned 401',
  responseBody: 'bad signature',
  durationMs: 12n,
  event: event ?? WebhookTriggerEvent.MAIL_SENT,
  payload: '{"event":"WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED","data":{}}',
}));

mock.module('../services/webhook', () => ({
  webhookService: { testWebhook },
  WebhookTriggerEvent,
}));

const { TestWebhookModal } = await import('./TestWebhookModal');

describe('TestWebhookModal', () => {
  test('sends the chosen event and shows how the endpoint answered', async () => {
    const webhook = create(WebhookSchema, {
      id: 'wh-1',
      url: 'https://example.com/hook',
      events: [WebhookTriggerEvent.MAIL_BOUNCED],
    });
    const view = render(
      <QueryClientProvider client={new QueryClient()}>
        <MantineProvider>
          <TestWebhookModal webhook={webhook} onClose={() => {}} />
        </MantineProvider>
      </QueryClientProvider>,
    );

    fireEvent.click(await view.findByRole('button', { name: /send test/i }));

    await waitFor(() => expect(view.getByText('HTTP 401')).toBeTruthy());
    // Defaults to the first event the webhook listens for.
    expect(testWebhook).toHaveBeenCalledWith('wh-1', WebhookTriggerEvent.MAIL_BOUNCED);
    expect(view.getByText('bad signature')).toBeTruthy();
    expect(view.getByText('Failed')).toBeTruthy();
  });
});
