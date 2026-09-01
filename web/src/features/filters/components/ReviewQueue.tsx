import React from 'react';
import {
  ActionIcon, Badge, Box, Button, Code, Group, Loader, Modal, Paper, ScrollArea,
  Select, Stack, Table, Text, Textarea, ThemeIcon, Title, Tooltip, rem,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  IconCheck, IconEye, IconFilter, IconPaperclip, IconX,
} from '@tabler/icons-react';
import { ConnectError, Code as ConnectCode } from '@connectrpc/connect';
import { filterService } from '../services/filter';
import {
  FilterAction, FilterDirection, FilterStatus,
  type FilteredMessage,
} from '../../../api/panmail/v1/email_filter_pb';

const STATUS_COLOURS: Record<number, string> = {
  [FilterStatus.PENDING]: 'yellow',
  [FilterStatus.RELEASED]: 'teal',
  [FilterStatus.REJECTED]: 'red',
  [FilterStatus.EXPIRED]: 'gray',
};

const STATUS_LABELS: Record<number, string> = {
  [FilterStatus.PENDING]: 'Pending',
  [FilterStatus.RELEASED]: 'Released',
  [FilterStatus.REJECTED]: 'Rejected',
  // Named for what it is. Nobody decided this one; the clock did, and a queue
  // full of them means the review process is not running.
  [FilterStatus.EXPIRED]: 'Expired unreviewed',
};

const ACTION_LABELS: Record<number, string> = {
  [FilterAction.HOLD]: 'Held',
  [FilterAction.REJECT]: 'Rejected on arrival',
  [FilterAction.TAG]: 'Tagged',
  [FilterAction.ALLOW]: 'Allowed',
};

/** A condition, phrased the way the rule author wrote it. */
function describeCondition(c: {
  field: string; operator: string; values: string[]; header: string; number: bigint;
}): string {
  const field = c.field === 'header' ? `header ${c.header}` : c.field.replace(/_/g, ' ');
  switch (c.operator) {
    case 'is_true':
      return field.replace(/^has /, 'has ');
    case 'exists':
      return `${field} is present`;
    case 'gte':
      return `${field} is at least ${c.number}`;
    case 'in':
      return `${field} is one of ${c.values.join(', ')}`;
    case 'domain_is':
      return `${field} domain is ${c.values.join(' or ')}`;
    case 'matches':
      return `${field} matches ${c.values.join(' or ')}`;
    default:
      return `${field} ${c.operator.replace(/_/g, ' ')} ${c.values.join(' or ')}`;
  }
}

function bytes(n: bigint | number): string {
  const size = Number(n);
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

export const ReviewQueue: React.FC = () => {
  const queryClient = useQueryClient();
  const [direction, setDirection] = React.useState<string>(String(FilterDirection.UNSPECIFIED));
  const [status, setStatus] = React.useState<string>(String(FilterStatus.PENDING));
  const [inspecting, setInspecting] = React.useState<FilteredMessage | null>(null);
  const [note, setNote] = React.useState('');

  const { data, isLoading } = useQuery({
    queryKey: ['filtered-messages', direction, status],
    queryFn: () => filterService.listMessages(Number(direction), Number(status)),
  });

  const close = () => {
    setInspecting(null);
    setNote('');
  };

  // Named as a hook because it calls one. Both are invoked unconditionally
  // and in the same order every render, which is what the rule actually
  // requires — the name is what lets the linter see that.
  const useDecision = (verb: 'release' | 'reject') =>
    useMutation({
      // Narrowed to the field both responses share. Returning the responses
      // themselves gives a union of two message types that no mutation can be
      // typed against.
      mutationFn: async ({ id, note }: { id: string; note: string }) => {
        const response =
          verb === 'release'
            ? await filterService.release(id, note)
            : await filterService.reject(id, note);
        return response.message;
      },
      onSuccess: () => {
        notifications.show({
          color: verb === 'release' ? 'teal' : 'orange',
          message: verb === 'release' ? 'Message released' : 'Message rejected',
        });
        void queryClient.invalidateQueries({ queryKey: ['filtered-messages'] });
        close();
      },
      onError: (error: unknown) => {
        // Losing the race is not a failure the reviewer caused, and saying
        // "internal error" would send them looking for a bug that is not there.
        const alreadyHandled =
          error instanceof ConnectError && error.code === ConnectCode.FailedPrecondition;
        notifications.show({
          color: alreadyHandled ? 'yellow' : 'red',
          title: alreadyHandled ? 'Already handled' : 'Could not complete',
          message: alreadyHandled
            ? 'Somebody else reviewed this message first.'
            : error instanceof Error ? error.message : 'Unknown error',
        });
        if (alreadyHandled) {
          void queryClient.invalidateQueries({ queryKey: ['filtered-messages'] });
          close();
        }
      },
    });

  const release = useDecision('release');
  const reject = useDecision('reject');
  const deciding = release.isPending || reject.isPending;

  const messages = data?.messages ?? [];

  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-end">
        <Group gap="xs">
          <ThemeIcon variant="light" color="brand" size="lg"><IconFilter size={18} /></ThemeIcon>
          <Box>
            <Title order={4}>Review queue</Title>
            <Text size="xs" c="dimmed">
              Messages a filter rule stopped. Releasing one sends it; rejecting one does not.
            </Text>
          </Box>
        </Group>
        <Group gap="sm">
          <Select
            label="Direction" size="xs" w={rem(150)} value={direction} onChange={(v) => setDirection(v ?? '0')}
            data={[
              { value: String(FilterDirection.UNSPECIFIED), label: 'Both' },
              { value: String(FilterDirection.OUTBOUND), label: 'Outbound' },
              { value: String(FilterDirection.INBOUND), label: 'Inbound' },
            ]}
          />
          <Select
            label="Status" size="xs" w={rem(180)} value={status} onChange={(v) => setStatus(v ?? '1')}
            data={[
              { value: String(FilterStatus.PENDING), label: 'Pending' },
              { value: String(FilterStatus.RELEASED), label: 'Released' },
              { value: String(FilterStatus.REJECTED), label: 'Rejected' },
              { value: String(FilterStatus.EXPIRED), label: 'Expired unreviewed' },
              { value: String(FilterStatus.UNSPECIFIED), label: 'All' },
            ]}
          />
        </Group>
      </Group>

      <Paper withBorder radius="md" p={0}>
        {isLoading ? (
          <Group justify="center" p="xl"><Loader size="sm" /></Group>
        ) : messages.length === 0 ? (
          <Stack align="center" gap="xs" p="xl">
            <ThemeIcon variant="light" color="gray" size="lg"><IconFilter size={18} /></ThemeIcon>
            <Text fw={600} size="sm">Nothing to review</Text>
            <Text size="xs" c="dimmed">No message matched these filters.</Text>
          </Stack>
        ) : (
          <ScrollArea>
            <Table highlightOnHover verticalSpacing="sm" horizontalSpacing="md">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Status</Table.Th>
                  <Table.Th>From</Table.Th>
                  <Table.Th>Subject</Table.Th>
                  <Table.Th>Rule</Table.Th>
                  <Table.Th>Size</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {messages.map((m) => (
                  <Table.Tr key={m.id}>
                    <Table.Td>
                      <Badge size="sm" variant="light" color={STATUS_COLOURS[m.status] ?? 'gray'}>
                        {STATUS_LABELS[m.status] ?? 'Unknown'}
                      </Badge>
                    </Table.Td>
                    <Table.Td><Text size="sm">{m.from}</Text></Table.Td>
                    <Table.Td>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" lineClamp={1}>{m.subject || <Text span c="dimmed">(no subject)</Text>}</Text>
                        {m.attachmentCount > 0 && (
                          <Tooltip label={`${m.attachmentCount} attachment(s)`}>
                            <ThemeIcon variant="subtle" color="gray" size="sm"><IconPaperclip size={13} /></ThemeIcon>
                          </Tooltip>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td><Text size="xs" c="dimmed">{m.ruleName}</Text></Table.Td>
                    <Table.Td><Text size="xs" c="dimmed">{bytes(m.sizeBytes)}</Text></Table.Td>
                    <Table.Td>
                      <ActionIcon variant="subtle" aria-label="Inspect" onClick={() => setInspecting(m)}>
                        <IconEye size={16} />
                      </ActionIcon>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        )}
      </Paper>

      <Modal opened={inspecting !== null} onClose={close} title="Filtered message" size="lg" radius="md">
        {inspecting && (
          <Stack gap="md">
            <Paper withBorder p="md" radius="md">
              <Stack gap={6}>
                <Group gap="xs">
                  <Badge size="sm" variant="light" color={STATUS_COLOURS[inspecting.status]}>
                    {STATUS_LABELS[inspecting.status]}
                  </Badge>
                  <Badge size="sm" variant="outline">{ACTION_LABELS[inspecting.action]}</Badge>
                  <Badge size="sm" variant="outline" color="gray">
                    {inspecting.direction === FilterDirection.INBOUND ? 'Inbound' : 'Outbound'}
                  </Badge>
                </Group>
                <Text size="sm"><b>From</b> {inspecting.from}</Text>
                <Text size="sm"><b>To</b> {inspecting.recipients.join(', ')}</Text>
                <Text size="sm"><b>Subject</b> {inspecting.subject || '(no subject)'}</Text>
                {inspecting.attachmentNames.length > 0 && (
                  <Text size="sm"><b>Attachments</b> {inspecting.attachmentNames.join(', ')}</Text>
                )}
              </Stack>
            </Paper>

            {/* "Held by rule 7" is not reviewable. This is why it was stopped. */}
            <Paper withBorder p="md" radius="md">
              <Text fw={700} size="sm" mb="xs">Why it was stopped</Text>
              <Text size="xs" c="dimmed" mb={6}>Rule: {inspecting.ruleName}</Text>
              <Stack gap={4}>
                {inspecting.matched.map((c, i) => (
                  <Code key={i} block style={{ fontSize: rem(11) }}>{describeCondition(c)}</Code>
                ))}
              </Stack>
            </Paper>

            {inspecting.status === FilterStatus.PENDING ? (
              <>
                <Textarea
                  label="Note" placeholder="Why are you releasing or rejecting this?"
                  description="Recorded against you in the audit trail."
                  value={note} onChange={(e) => setNote(e.currentTarget.value)} autosize minRows={2}
                />
                <Group justify="flex-end">
                  <Button
                    variant="light" color="red" leftSection={<IconX size={16} />} loading={reject.isPending}
                    disabled={deciding}
                    onClick={() => reject.mutate({ id: inspecting.id, note })}
                  >
                    Reject
                  </Button>
                  <Button
                    color="teal" leftSection={<IconCheck size={16} />} loading={release.isPending}
                    disabled={deciding}
                    onClick={() => release.mutate({ id: inspecting.id, note })}
                  >
                    Release
                  </Button>
                </Group>
              </>
            ) : (
              <Paper withBorder p="md" radius="md" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))">
                <Text size="sm">
                  {STATUS_LABELS[inspecting.status]}
                  {inspecting.reviewedBy && <> by <b>{inspecting.reviewedBy}</b></>}
                </Text>
                {inspecting.reviewNote && <Text size="xs" c="dimmed" mt={4}>{inspecting.reviewNote}</Text>}
              </Paper>
            )}
          </Stack>
        )}
      </Modal>
    </Stack>
  );
};
