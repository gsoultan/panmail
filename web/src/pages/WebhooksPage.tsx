import React, { useState, useEffect } from 'react';
import { 
  Container, Title, Button, Group, Stack, Modal, Box, Text, rem, ThemeIcon, 
  Table, ActionIcon, Badge, TextInput, Switch, MultiSelect, useComputedColorScheme, Paper, Select
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconWebhook, IconPlus, IconEdit, IconTrash, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useForm } from '@mantine/form';
import { webhookService, WebhookTriggerEvent } from '../features/webhooks/services/webhook';
import { Webhook } from '../api/panmail/v1/webhook_pb';

const triggerEventOptions = [
  { value: WebhookTriggerEvent.MAIL_SENT.toString(), label: 'Mail Sent' },
  { value: WebhookTriggerEvent.MAIL_DELIVERED.toString(), label: 'Mail Delivered' },
  { value: WebhookTriggerEvent.MAIL_OPENED.toString(), label: 'Mail Opened' },
  { value: WebhookTriggerEvent.MAIL_CLICKED.toString(), label: 'Mail Clicked' },
  { value: WebhookTriggerEvent.MAIL_BOUNCED.toString(), label: 'Mail Bounced' },
  { value: WebhookTriggerEvent.MAIL_REJECTED.toString(), label: 'Mail Rejected' },
  { value: WebhookTriggerEvent.MAIL_INBOUND.toString(), label: 'Mail Inbound' },
];

const triggerEventLabels: Record<number, string> = {
  [WebhookTriggerEvent.UNSPECIFIED]: 'Unspecified',
  [WebhookTriggerEvent.MAIL_SENT]: 'Sent',
  [WebhookTriggerEvent.MAIL_DELIVERED]: 'Delivered',
  [WebhookTriggerEvent.MAIL_OPENED]: 'Opened',
  [WebhookTriggerEvent.MAIL_CLICKED]: 'Clicked',
  [WebhookTriggerEvent.MAIL_BOUNCED]: 'Bounced',
  [WebhookTriggerEvent.MAIL_REJECTED]: 'Rejected',
  [WebhookTriggerEvent.MAIL_INBOUND]: 'Inbound',
};

export const WebhooksPage: React.FC = () => {
  const queryClient = useQueryClient();
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [editingWebhook, setEditingWebhook] = useState<Webhook | null>(null);
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('10');

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const { data, isLoading } = useQuery({
    queryKey: ['webhooks', pageToken, pageSize],
    queryFn: () => webhookService.listWebhooks(Number(pageSize), pageToken),
  });

  const webhooks = data?.webhooks || [];
  const nextPageToken = data?.nextPageToken;

  const handleNext = () => {
    if (nextPageToken) {
      setHistory([...history, pageToken ?? '']);
      setPageToken(nextPageToken);
    }
  };

  const handlePrev = () => {
    const newHistory = [...history];
    const prev = newHistory.pop();
    setHistory(newHistory);
    setPageToken(prev === '' ? undefined : prev);
  };

  const form = useForm({
    initialValues: {
      name: '',
      url: '',
      events: [] as string[],
      active: true,
    },
    validate: {
      name: (value) => (value.length < 2 ? 'Name must have at least 2 characters' : null),
      url: (value) => (/^https?:\/\/.+/.test(value) ? null : 'Invalid URL'),
      events: (value) => (value.length === 0 ? 'Select at least one event' : null),
    },
  });

  const createMutation = useMutation({
    mutationFn: (values: typeof form.values) => webhookService.createWebhook({
      name: values.name,
      url: values.url,
      events: values.events.map(e => parseInt(e)),
    }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['webhooks'] });
      notifications.show({ title: 'Success', message: 'Webhook created successfully', color: 'green' });
      handleClose();
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to create webhook', color: 'red' });
    },
  });

  const updateMutation = useMutation({
    mutationFn: (values: typeof form.values) => webhookService.updateWebhook({
      id: editingWebhook?.id,
      name: values.name,
      url: values.url,
      events: values.events.map(e => parseInt(e)),
      active: values.active,
    }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['webhooks'] });
      notifications.show({ title: 'Success', message: 'Webhook updated successfully', color: 'green' });
      handleClose();
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to update webhook', color: 'red' });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => webhookService.deleteWebhook(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['webhooks'] });
      notifications.show({ title: 'Success', message: 'Webhook deleted successfully', color: 'green' });
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to delete webhook', color: 'red' });
    },
  });

  const handleEdit = (webhook: Webhook) => {
    setEditingWebhook(webhook);
    form.setValues({
      name: webhook.name,
      url: webhook.url,
      events: webhook.events.map(e => e.toString()),
      active: webhook.active,
    });
    setIsModalOpen(true);
  };

  const handleClose = () => {
    setIsModalOpen(false);
    setEditingWebhook(null);
    form.reset();
  };

  const handleSubmit = (values: typeof form.values) => {
    if (editingWebhook) {
      updateMutation.mutate(values);
    } else {
      createMutation.mutate(values);
    }
  };

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Group justify="space-between" align="flex-end">
          <Box>
            <Group gap="xs" mb={4}>
              <ThemeIcon variant="light" color="brand" size="md">
                <IconWebhook size={18} />
              </ThemeIcon>
              <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Webhooks</Title>
            </Group>
            <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Configure outbound webhooks to receive real-time notifications for mail events.</Text>
          </Box>
          <Group gap="md">
            <Select
              label="Page Size"
              size="xs"
              data={['10', '20', '50', '100']}
              value={pageSize}
              onChange={setPageSize}
              style={{ width: rem(80) }}
            />
            <Button
              onClick={() => setIsModalOpen(true)}
              leftSection={<IconPlus size={18} />}
              color="brand"
              radius="md"
              size="sm"
            >
              Add Webhook
            </Button>
          </Group>
        </Group>

        <Box style={{
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))',
          borderRadius: rem(12),
          border: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))',
          overflow: 'hidden'
        }}>
          <Table verticalSpacing="md" horizontalSpacing="lg">
            <Table.Thead style={{ backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))' }}>
              <Table.Tr>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Name</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">URL</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Events</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Status</Text></Table.Th>
                <Table.Th style={{ textAlign: 'right' }}><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Actions</Text></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {webhooks.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={5} style={{ textAlign: 'center', padding: rem(40) }}>
                    <Text c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={500}>No webhooks configured. Add one to start receiving notifications.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                webhooks.map((w) => (
                  <Table.Tr key={w.id} style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
                    <Table.Td>
                      <Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{w.name}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-1))" style={{ fontFamily: 'monospace' }}>{w.url}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4}>
                        {w.events.map(e => (
                          <Badge key={e} size="xs" variant="outline" color="gray" radius="xs">
                            {triggerEventLabels[e] || 'Unknown'}
                          </Badge>
                        ))}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={w.active ? 'green' : 'gray'} radius="sm">
                        {w.active ? 'Active' : 'Inactive'}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Group gap="xs" justify="flex-end">
                        <ActionIcon variant="light" color="brand" onClick={() => handleEdit(w)}>
                          <IconEdit size={16} />
                        </ActionIcon>
                        <ActionIcon variant="light" color="red" onClick={() => {
                          if (window.confirm('Delete this webhook?')) {
                            deleteMutation.mutate(w.id);
                          }
                        }}>
                          <IconTrash size={16} />
                        </ActionIcon>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))
              )}
            </Table.Tbody>
          </Table>
        </Box>

        {(nextPageToken || history.length > 0) && (
          <Group justify="space-between" align="center">
            <Text size="sm" c="dimmed" fw={500}>
              Showing page <Text span fw={700} c="brand">{history.length + 1}</Text>
            </Text>
            <Group gap="xs">
              <Button 
                variant="light" 
                onClick={handlePrev} 
                disabled={history.length === 0}
                leftSection={<IconChevronLeft size={14} />}
                size="xs"
                radius="md"
              >
                Previous
              </Button>
              <Button 
                variant="light" 
                onClick={handleNext} 
                disabled={!nextPageToken}
                rightSection={<IconChevronRight size={14} />}
                size="xs"
                radius="md"
              >
                Next
              </Button>
            </Group>
          </Group>
        )}
      </Stack>

      <Modal 
        opened={isModalOpen} 
        onClose={handleClose} 
        title={<Text fw={800} size="lg">{editingWebhook ? 'Edit Webhook' : 'Add Webhook'}</Text>}
        radius="md"
        size="md"
      >
        <form onSubmit={form.onSubmit(handleSubmit)}>
          <Stack>
            <TextInput
              label="Name"
              placeholder="e.g. My Application Webhook"
              required
              {...form.getInputProps('name')}
            />
            <TextInput
              label="URL"
              placeholder="https://api.myapp.com/webhooks/panmail"
              required
              {...form.getInputProps('url')}
            />
            <MultiSelect
              label="Events"
              placeholder="Select trigger events"
              data={triggerEventOptions}
              required
              {...form.getInputProps('events')}
            />
            <Switch
              label="Active"
              {...form.getInputProps('active', { type: 'checkbox' })}
            />
            <Group justify="flex-end" mt="md">
              <Button variant="subtle" color="gray" onClick={handleClose}>Cancel</Button>
              <Button color="brand" type="submit" loading={createMutation.isPending || updateMutation.isPending}>
                {editingWebhook ? 'Update Webhook' : 'Create Webhook'}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Container>
  );
};
