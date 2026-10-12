import React, { useEffect, useState } from 'react';
import { Alert, Badge, Button, Group, Modal, Select, Stack, Text, Code, rem } from '@mantine/core';
import { IconSend } from '@tabler/icons-react';
import { useMutation } from '@tanstack/react-query';
import type { Webhook } from '../../../api/panmail/v1/webhook_pb';
import { webhookService } from '../services/webhook';
import { triggerEventOptions } from '../events';
import { JsonBlock } from './JsonBlock';

interface Props {
  webhook: Webhook | null;
  onClose: () => void;
}

// Sends one signed sample notification to a webhook's URL and shows how the
// endpoint answered, so a receiver can be checked before real mail flows.
export const TestWebhookModal: React.FC<Props> = ({ webhook, onClose }) => {
  const [event, setEvent] = useState<string | null>(null);

  // The events this webhook listens for come first; any event can be tried.
  const subscribed = new Set((webhook?.events ?? []).map((e) => e.toString()));
  const options = [
    ...triggerEventOptions.filter((o) => subscribed.has(o.value)),
    ...triggerEventOptions.filter((o) => !subscribed.has(o.value)),
  ];

  const mutation = useMutation({
    mutationFn: () => webhookService.testWebhook(webhook!.id, event ? parseInt(event) : undefined),
  });

  useEffect(() => {
    setEvent(webhook?.events[0]?.toString() ?? null);
    mutation.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [webhook?.id]);

  const result = mutation.data;

  return (
    <Modal
      opened={webhook !== null}
      onClose={onClose}
      title={<Text fw={800} size="lg">Test Webhook</Text>}
      radius="md"
      size="lg"
    >
      <Stack>
        <Text size="sm" c="dimmed">
          Sends a signed sample notification to{' '}
          <Code style={{ overflowWrap: 'anywhere' }}>{webhook?.url}</Code> right away. It is not
          queued or retried.
        </Text>

        <Group align="flex-end">
          <Select
            label="Event"
            data={options}
            value={event}
            onChange={setEvent}
            allowDeselect={false}
            style={{ flex: 1 }}
          />
          <Button
            color="brand"
            leftSection={<IconSend size={16} />}
            loading={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            Send test
          </Button>
        </Group>

        {mutation.error && (
          <Alert color="red" variant="light">
            {(mutation.error as Error).message || 'Failed to send the test'}
          </Alert>
        )}

        {result && (
          <Stack gap="xs">
            <Group gap="xs">
              <Badge color={result.success ? 'green' : 'red'} variant="light" radius="sm">
                {result.success ? 'Delivered' : 'Failed'}
              </Badge>
              {result.statusCode > 0 && (
                <Badge color="gray" variant="outline" radius="sm">HTTP {result.statusCode}</Badge>
              )}
              <Text size="xs" c="dimmed">{result.durationMs.toString()} ms</Text>
            </Group>
            {result.error && <Text size="sm" c="red">{result.error}</Text>}
            {result.responseBody && (
              <>
                <Text size="xs" fw={700}>Response body</Text>
                <Code block style={{ whiteSpace: 'pre-wrap', fontSize: rem(12), maxHeight: rem(160), overflow: 'auto' }}>
                  {result.responseBody}
                </Code>
              </>
            )}
            <Text size="xs" fw={700}>Payload sent</Text>
            <JsonBlock value={prettyJson(result.payload)} />
          </Stack>
        )}
      </Stack>
    </Modal>
  );
};

const prettyJson = (raw: string): string => {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
};
