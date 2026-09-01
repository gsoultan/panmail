import React from 'react';
import {
  ActionIcon, Badge, Box, Button, Group, Loader, Modal, NumberInput, Paper, ScrollArea,
  Select, Stack, Switch, Table, Text, TextInput, ThemeIcon, Title, rem,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { IconPlus, IconShieldCheck, IconTrash, IconX } from '@tabler/icons-react';
import { filterService } from '../services/filter';
import {
  FilterAction, FilterDirection, type FilterRule,
} from '../../../api/panmail/v1/email_filter_pb';

// The catalogue, grouped the way someone writing a rule thinks about it rather
// than the way the evaluator stores it.
const FIELDS = [
  { group: 'Sender', items: [['from', 'From address']] },
  {
    group: 'Recipients',
    items: [['to', 'To'], ['cc', 'Cc'], ['bcc', 'Bcc'], ['any_recipient', 'Any recipient'],
      ['recipient_count', 'Recipient count']],
  },
  {
    group: 'Content',
    items: [['subject', 'Subject'], ['body', 'Body'], ['subject_or_body', 'Subject or body'],
      ['header', 'A header']],
  },
  {
    group: 'Attachments',
    items: [['has_attachment', 'Has any attachment'], ['has_executable_attachment', 'Has an executable'],
      ['attachment_name', 'Attachment name'], ['attachment_extension', 'Attachment extension'],
      ['attachment_count', 'Attachment count'], ['attachment_size', 'Largest attachment'],
      ['total_attachment_size', 'Total attachment size']],
  },
  { group: 'Message', items: [['message_size', 'Message size'], ['provider', 'Provider']] },
  { group: 'Inbound authentication', items: [['spf', 'SPF'], ['dkim', 'DKIM'], ['dmarc', 'DMARC']] },
] as const;

// Which operators a field accepts. Mirrors the server's table; sending an
// illegal pair is refused with invalid_argument, so this is about not offering
// it rather than about enforcement.
const NUMERIC = new Set(['attachment_count', 'attachment_size', 'total_attachment_size', 'message_size', 'recipient_count']);
const BOOLEAN = new Set(['has_attachment', 'has_executable_attachment']);
const ENUMS = new Set(['provider', 'spf', 'dkim', 'dmarc']);
const ADDRESSES = new Set(['from', 'to', 'cc', 'bcc', 'any_recipient']);

function operatorsFor(field: string): { value: string; label: string }[] {
  if (NUMERIC.has(field)) return [{ value: 'gte', label: 'is at least' }];
  if (BOOLEAN.has(field)) return [{ value: 'is_true', label: 'is true' }];
  if (ENUMS.has(field)) return [{ value: 'equals', label: 'is' }, { value: 'in', label: 'is one of' }];
  if (field === 'header') {
    return [{ value: 'exists', label: 'exists' }, { value: 'contains', label: 'contains' },
      { value: 'matches', label: 'matches pattern' }, { value: 'equals', label: 'is' }];
  }
  const base = [{ value: 'contains', label: 'contains' }, { value: 'matches', label: 'matches pattern' },
    { value: 'equals', label: 'is' }];
  if (ADDRESSES.has(field)) base.push({ value: 'domain_is', label: 'domain is' });
  else base.push({ value: 'in', label: 'is one of' });
  return base;
}

type DraftCondition = {
  field: string; operator: string; values: string; header: string; number: number;
};

const emptyCondition = (): DraftCondition =>
  ({ field: 'from', operator: 'contains', values: '', header: '', number: 0 });

const ACTIONS = [
  { value: String(FilterAction.HOLD), label: 'Hold for review' },
  { value: String(FilterAction.REJECT), label: 'Reject outright' },
  { value: String(FilterAction.TAG), label: 'Tag and deliver' },
  { value: String(FilterAction.ALLOW), label: 'Allow and stop evaluating' },
];

export const RuleList: React.FC = () => {
  const queryClient = useQueryClient();
  const [creating, setCreating] = React.useState(false);
  const [name, setName] = React.useState('');
  const [direction, setDirection] = React.useState(String(FilterDirection.OUTBOUND));
  const [action, setAction] = React.useState(String(FilterAction.HOLD));
  const [priority, setPriority] = React.useState(100);
  const [conditions, setConditions] = React.useState<DraftCondition[]>([emptyCondition()]);

  const { data, isLoading } = useQuery({
    queryKey: ['filter-rules'],
    queryFn: () => filterService.listRules(),
  });

  const reset = () => {
    setCreating(false);
    setName(''); setPriority(100); setConditions([emptyCondition()]);
    setDirection(String(FilterDirection.OUTBOUND)); setAction(String(FilterAction.HOLD));
  };

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['filter-rules'] });

  const create = useMutation({
    mutationFn: () =>
      filterService.createRule({
        name,
        direction: Number(direction),
        action: Number(action),
        priority,
        enabled: true,
        conditions: conditions.map((c) => ({
          field: c.field,
          operator: c.operator,
          // Split on commas so one condition can carry several values, which
          // the engine ORs together.
          values: c.values.split(',').map((v) => v.trim()).filter(Boolean),
          header: c.header,
          number: BigInt(c.number || 0),
        })),
      }),
    onSuccess: () => {
      notifications.show({ color: 'teal', message: 'Rule created' });
      void invalidate();
      reset();
    },
    onError: (error: unknown) =>
      notifications.show({
        color: 'red', title: 'Could not save the rule',
        message: error instanceof Error ? error.message : 'Unknown error',
      }),
  });

  const toggle = useMutation({
    mutationFn: (rule: FilterRule) => filterService.updateRule({ ...rule, enabled: !rule.enabled }),
    onSuccess: () => void invalidate(),
  });

  const remove = useMutation({
    mutationFn: (id: string) => filterService.deleteRule(id),
    onSuccess: () => {
      notifications.show({ color: 'orange', message: 'Rule deleted' });
      void invalidate();
    },
  });

  const rules = data?.rules ?? [];

  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Group gap="xs">
          <ThemeIcon variant="light" color="brand" size="lg"><IconShieldCheck size={18} /></ThemeIcon>
          <Box>
            <Title order={4}>Filter rules</Title>
            <Text size="xs" c="dimmed">
              Conditions are combined with AND. The lowest priority that matches decides.
            </Text>
          </Box>
        </Group>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>New rule</Button>
      </Group>

      <Paper withBorder radius="md" p={0}>
        {isLoading ? (
          <Group justify="center" p="xl"><Loader size="sm" /></Group>
        ) : rules.length === 0 ? (
          <Stack align="center" gap="xs" p="xl">
            <Text fw={600} size="sm">No filter rules</Text>
            <Text size="xs" c="dimmed">Nothing is filtered until you add one.</Text>
          </Stack>
        ) : (
          <ScrollArea>
            <Table highlightOnHover verticalSpacing="sm" horizontalSpacing="md">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Enabled</Table.Th>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Direction</Table.Th>
                  <Table.Th>Action</Table.Th>
                  <Table.Th>Priority</Table.Th>
                  <Table.Th>Conditions</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {rules.map((rule) => (
                  <Table.Tr key={rule.id}>
                    <Table.Td>
                      <Switch
                        size="sm" checked={rule.enabled}
                        aria-label={`Enable ${rule.name}`}
                        onChange={() => toggle.mutate(rule)}
                      />
                    </Table.Td>
                    <Table.Td><Text size="sm" fw={500}>{rule.name}</Text></Table.Td>
                    <Table.Td>
                      <Badge size="sm" variant="light" color={rule.direction === FilterDirection.INBOUND ? 'grape' : 'blue'}>
                        {rule.direction === FilterDirection.INBOUND ? 'Inbound' : 'Outbound'}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Badge size="sm" variant="outline">
                        {ACTIONS.find((a) => a.value === String(rule.action))?.label ?? '—'}
                      </Badge>
                    </Table.Td>
                    <Table.Td><Text size="xs" c="dimmed">{rule.priority}</Text></Table.Td>
                    <Table.Td><Text size="xs" c="dimmed">{rule.conditions.length}</Text></Table.Td>
                    <Table.Td>
                      <ActionIcon
                        variant="subtle" color="red" aria-label={`Delete ${rule.name}`}
                        onClick={() => remove.mutate(rule.id)}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        )}
      </Paper>

      <Modal opened={creating} onClose={reset} title="New filter rule" size="lg" radius="md">
        <Stack gap="md">
          <TextInput label="Name" placeholder="Hold large attachments to partners" required
            value={name} onChange={(e) => setName(e.currentTarget.value)} />

          <Group grow>
            <Select label="Direction" value={direction} onChange={(v) => setDirection(v ?? direction)}
              data={[
                { value: String(FilterDirection.OUTBOUND), label: 'Outbound' },
                { value: String(FilterDirection.INBOUND), label: 'Inbound' },
              ]} />
            <Select label="Action" value={action} onChange={(v) => setAction(v ?? action)} data={ACTIONS} />
            <NumberInput label="Priority" description="Lowest first" value={priority}
              onChange={(v) => setPriority(Number(v) || 0)} min={0} />
          </Group>

          <Box>
            <Group justify="space-between" mb="xs">
              <Text fw={700} size="sm">Conditions</Text>
              <Button size="compact-xs" variant="light" leftSection={<IconPlus size={12} />}
                onClick={() => setConditions((c) => [...c, emptyCondition()])}>
                Add
              </Button>
            </Group>
            <Text size="xs" c="dimmed" mb="xs">
              All of these must hold. Separate several values with commas — any one of them matches.
            </Text>

            <Stack gap="xs">
              {conditions.map((condition, i) => {
                const operators = operatorsFor(condition.field);
                const needsValues = !['is_true', 'exists', 'gte'].includes(condition.operator);
                return (
                  <Paper key={i} withBorder p="xs" radius="sm">
                    <Group gap="xs" align="flex-end" wrap="nowrap">
                      <Select
                        label={i === 0 ? 'Field' : undefined} size="xs" w={rem(190)} searchable
                        value={condition.field}
                        data={FIELDS.map((g) => ({
                          group: g.group,
                          items: g.items.map(([value, label]) => ({ value, label })),
                        }))}
                        onChange={(v) => setConditions((all) => all.map((c, j) =>
                          j === i ? { ...c, field: v ?? c.field, operator: operatorsFor(v ?? c.field)[0].value } : c))}
                      />
                      <Select
                        label={i === 0 ? 'Is' : undefined} size="xs" w={rem(150)}
                        value={condition.operator} data={operators}
                        onChange={(v) => setConditions((all) => all.map((c, j) =>
                          j === i ? { ...c, operator: v ?? c.operator } : c))}
                      />
                      {condition.field === 'header' && (
                        <TextInput label={i === 0 ? 'Header' : undefined} size="xs" w={rem(140)}
                          placeholder="X-Spam-Score" value={condition.header}
                          onChange={(e) => setConditions((all) => all.map((c, j) =>
                            j === i ? { ...c, header: e.currentTarget.value } : c))} />
                      )}
                      {condition.operator === 'gte' ? (
                        <NumberInput label={i === 0 ? 'Value' : undefined} size="xs" style={{ flex: 1 }}
                          value={condition.number} min={0}
                          onChange={(v) => setConditions((all) => all.map((c, j) =>
                            j === i ? { ...c, number: Number(v) || 0 } : c))} />
                      ) : needsValues ? (
                        <TextInput label={i === 0 ? 'Value' : undefined} size="xs" style={{ flex: 1 }}
                          placeholder="invoice, receipt" value={condition.values}
                          onChange={(e) => setConditions((all) => all.map((c, j) =>
                            j === i ? { ...c, values: e.currentTarget.value } : c))} />
                      ) : (
                        <Box style={{ flex: 1 }} />
                      )}
                      <ActionIcon variant="subtle" color="red" aria-label="Remove condition"
                        disabled={conditions.length === 1}
                        onClick={() => setConditions((all) => all.filter((_, j) => j !== i))}>
                        <IconX size={14} />
                      </ActionIcon>
                    </Group>
                  </Paper>
                );
              })}
            </Stack>
          </Box>

          <Group justify="flex-end">
            <Button variant="subtle" onClick={reset}>Cancel</Button>
            <Button loading={create.isPending} disabled={!name.trim()} onClick={() => create.mutate()}>
              Create rule
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
};
