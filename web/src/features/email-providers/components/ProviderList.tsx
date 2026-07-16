import React from 'react';
import { Table, Group, Text, Badge, ActionIcon, Stack, Paper, rem, useComputedColorScheme, CopyButton } from '@mantine/core';
import { IconTrash, IconEdit, IconPlayerPlay, IconSend, IconCheck, IconCopy } from '@tabler/icons-react';
import type { EmailProvider } from '../../../api/panmail/v1/email_provider_pb';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';

interface ProviderListProps {
  providers: EmailProvider[];
  onEdit: (provider: EmailProvider) => void;
  onDelete: (id: string) => void;
  onTest: (id: string) => void;
  onSendTest: (id: string) => void;
}

export const ProviderList: React.FC<ProviderListProps> = ({ providers, onEdit, onDelete, onTest, onSendTest }) => {
  const getProviderTypeName = (type: ProviderType) => {
    return ProviderType[type].replace('PROVIDER_TYPE_', '');
  };

  const rows = providers.map((provider) => (
    <Table.Tr key={provider.id} style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
      <Table.Td>
        <Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{provider.name}</Text>
      </Table.Td>
      <Table.Td>
        <Group gap="xs">
          <Text size="xs" c="dimmed" ff="monospace" style={{ fontSize: rem(10) }}>{provider.id}</Text>
          <CopyButton value={provider.id}>
            {({ copied, copy }: { copied: boolean; copy: () => void }) => (
              <ActionIcon color={copied ? 'teal' : 'gray'} variant="subtle" onClick={copy} size="xs">
                {copied ? <IconCheck size={12} stroke={2} /> : <IconCopy size={12} stroke={2} />}
              </ActionIcon>
            )}
          </CopyButton>
        </Group>
      </Table.Td>
      <Table.Td>
        <Badge variant="light" color="brand" radius="sm" size="sm" fw={700}>
          {getProviderTypeName(provider.type)}
        </Badge>
      </Table.Td>
      <Table.Td>
        <Group gap="xs" justify="flex-end">
          <ActionIcon
            variant="light"
            color="green"
            onClick={() => onSendTest(provider.id)}
            title="Send Test Email"
            radius="md"
          >
            <IconSend size={16} stroke={2} />
          </ActionIcon>
          <ActionIcon
            variant="light"
            color="brand"
            onClick={() => onTest(provider.id)}
            title="Test Connection (Ping)"
            radius="md"
          >
            <IconPlayerPlay size={16} stroke={2} />
          </ActionIcon>
          <ActionIcon
            variant="light"
            color="gray"
            onClick={() => onEdit(provider)}
            title="Edit"
            radius="md"
          >
            <IconEdit size={16} stroke={2} />
          </ActionIcon>
          <ActionIcon
            variant="light"
            color="red"
            onClick={() => onDelete(provider.id)}
            title="Delete"
            radius="md"
          >
            <IconTrash size={16} stroke={2} />
          </ActionIcon>
        </Group>
      </Table.Td>
    </Table.Tr>
  ));

  return (
    <Paper withBorder radius="md" style={{ overflow: 'hidden', backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))', borderColor: 'light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
      <Table verticalSpacing="md" horizontalSpacing="lg">
        <Table.Thead style={{ backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))' }}>
          <Table.Tr>
            <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Name</Text></Table.Th>
            <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Provider ID</Text></Table.Th>
            <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Type</Text></Table.Th>
            <Table.Th style={{ textAlign: 'right' }}><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Actions</Text></Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {providers.length > 0 ? rows : (
            <Table.Tr>
              <Table.Td colSpan={4}>
                <Text ta="center" c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={600} py={40}>
                  No email providers configured yet. Click "Add Provider" to get started.
                </Text>
              </Table.Td>
            </Table.Tr>
          )}
        </Table.Tbody>
      </Table>
    </Paper>
  );
};
