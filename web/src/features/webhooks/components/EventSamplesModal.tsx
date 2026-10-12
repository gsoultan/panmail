import React from 'react';
import { Accordion, Alert, Code, Loader, Modal, Stack, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { webhookService } from '../services/webhook';
import { eventLabel } from './eventLabel';
import { JsonBlock } from './JsonBlock';

interface Props {
  opened: boolean;
  onClose: () => void;
}

// What a subscriber receives for each event: the JSON body, built by the
// server from the same types real notifications use, and the headers that
// come with it.
export const EventSamplesModal: React.FC<Props> = ({ opened, onClose }) => {
  const { data, isLoading, error } = useQuery({
    queryKey: ['webhook-event-samples'],
    queryFn: () => webhookService.listEventSamples(),
    enabled: opened,
    staleTime: Infinity,
  });

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={<Text fw={800} size="lg">Sample Payloads</Text>}
      radius="md"
      size="xl"
    >
      <Stack>
        <Alert variant="light" color="brand" icon={<IconInfoCircle size={18} />}>
          <Text size="sm">
            Each notification is a JSON <Code>POST</Code> with these headers:{' '}
            <Code>X-Panmail-Event</Code> (the event name), <Code>X-Panmail-Delivery</Code> (stable
            across retries, for deduplication), <Code>X-Panmail-Timestamp</Code> and{' '}
            <Code>X-Panmail-Signature</Code>. The signature is <Code>sha256=</Code> followed by the
            hex HMAC-SHA256 of <Code>{'{timestamp}.{body}'}</Code> keyed with the webhook's secret.
            Test sends also carry <Code>X-Panmail-Test: true</Code>.
          </Text>
        </Alert>

        {isLoading && <Loader size="sm" />}
        {error && (
          <Text c="red" size="sm">
            {(error as Error).message || 'Failed to load sample payloads'}
          </Text>
        )}

        {data && (
          <Accordion variant="separated" radius="md">
            {data.samples.map((s) => (
              <Accordion.Item key={s.event} value={s.event.toString()}>
                <Accordion.Control>
                  <Text fw={700} size="sm">{eventLabel(s.event)}</Text>
                  <Text size="xs" c="dimmed">{s.description}</Text>
                </Accordion.Control>
                <Accordion.Panel>
                  <JsonBlock value={s.payload} />
                </Accordion.Panel>
              </Accordion.Item>
            ))}
          </Accordion>
        )}
      </Stack>
    </Modal>
  );
};
