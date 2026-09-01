import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConnectError, Code } from '@connectrpc/connect';

const held = {
  id: 'q1',
  direction: 1,
  ruleId: 'r1',
  ruleName: 'large attachments',
  action: 1,
  status: 1,
  messageId: 'm1',
  providerId: 'p1',
  from: 'alice@example.com',
  recipients: ['bob@partner.net'],
  subject: 'Quarterly numbers',
  sizeBytes: 5_000_000n,
  attachmentCount: 1,
  attachmentNames: ['numbers.xlsx'],
  matched: [
    { field: 'has_attachment', operator: 'is_true', values: [], header: '', number: 0n },
    { field: 'message_size', operator: 'gte', values: [], header: '', number: 1_000_000n },
  ],
  reviewedBy: '',
  reviewNote: '',
};

let releaseResult: () => Promise<unknown> = async () => ({ message: { ...held, status: 2 } });
const released: string[] = [];
const rejected: string[] = [];

// Asserting on the call rather than on rendered output: the notification needs
// its provider mounted, and what matters is which message the reviewer is
// shown, not where it lands on screen.
const notified: { color?: string; title?: string; message?: string }[] = [];
mock.module('@mantine/notifications', () => ({
  notifications: { show: (n: { color?: string; title?: string; message?: string }) => notified.push(n) },
}));

mock.module('../services/filter', () => ({
  filterService: {
    listMessages: async () => ({ messages: [held], nextPageToken: '' }),
    release: async (id: string) => {
      released.push(id);
      return releaseResult();
    },
    reject: async (id: string) => {
      rejected.push(id);
      return { message: { ...held, status: 3 } };
    },
  },
}));

const { ReviewQueue } = await import('./ReviewQueue');

const renderQueue = async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  let result!: ReturnType<typeof render>;
  await act(async () => {
    result = render(
      <QueryClientProvider client={client}>
        <MantineProvider>
          <ReviewQueue />
        </MantineProvider>
      </QueryClientProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  return result;
};

// Mantine's Modal animates in, so the buttons inside it are not in the DOM on
// the tick after the click.
const openInspector = async () => {
  await act(async () => {
    fireEvent.click(screen.getByLabelText('Inspect'));
    await new Promise((resolve) => setTimeout(resolve, 60));
  });
};

describe('ReviewQueue', () => {
  test('lists what a rule held', async () => {
    await renderQueue();
    expect(screen.getByText('Quarterly numbers')).toBeTruthy();
    expect(screen.getByText('large attachments')).toBeTruthy();
  });

  // "Held by rule 7" is not reviewable. The conditions that fired are.
  test('explains why the message was stopped', async () => {
    await renderQueue();
    await openInspector();

    const shown = document.body.textContent ?? '';
    expect(shown).toContain('has attachment');
    expect(shown).toContain('message size is at least 1000000');
  });

  test('releasing calls the service with the message id', async () => {
    released.length = 0;
    await renderQueue();
    await openInspector();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Release' }));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(released).toEqual(['q1']);
  });

  test('rejecting calls the service with the message id', async () => {
    rejected.length = 0;
    await renderQueue();
    await openInspector();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Reject' }));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(rejected).toEqual(['q1']);
  });

  // Losing the race is not a failure the reviewer caused, and calling it an
  // internal error would send them hunting a bug that is not there.
  test('a lost race reads as already handled, not as an error', async () => {
    notified.length = 0;
    releaseResult = async () => {
      throw new ConnectError('already reviewed', Code.FailedPrecondition);
    };
    await renderQueue();
    await openInspector();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Release' }));
      await new Promise((resolve) => setTimeout(resolve, 10));
    });

    expect(notified).toHaveLength(1);
    expect(notified[0]!.title).toBe('Already handled');
    expect(notified[0]!.color).toBe('yellow');

    releaseResult = async () => ({ message: { ...held, status: 2 } });
  });
});
