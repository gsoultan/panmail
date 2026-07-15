import React, { useState, useEffect } from 'react';
import { Container, Title, Button, Group, Stack, Modal, Box, Text, rem, ThemeIcon, Table, ActionIcon, Badge, TextInput, Textarea, useComputedColorScheme, Select } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconShieldCancel, IconPlus, IconTrash, IconSearch, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useForm } from '@mantine/form';
import { suppressionService } from '../features/suppressions/services/suppression';

export const SuppressionsPage: React.FC = () => {
  const queryClient = useQueryClient();
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('50');

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const { data, isLoading } = useQuery({
    queryKey: ['suppressions', pageToken, pageSize],
    queryFn: () => suppressionService.listSuppressions(Number(pageSize), pageToken),
  });

  const suppressions = data?.suppressions || [];
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

  const addMutation = useMutation({
    mutationFn: (values: any) => suppressionService.addSuppression(values),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['suppressions'] });
      notifications.show({ title: 'Success', message: 'Email address suppressed successfully', color: 'green' });
      setIsModalOpen(false);
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to add suppression', color: 'red' });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (email: string) => suppressionService.removeSuppression(email),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['suppressions'] });
      notifications.show({ title: 'Success', message: 'Suppression removed successfully', color: 'green' });
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to remove suppression', color: 'red' });
    },
  });

  const form = useForm({
    initialValues: {
      email: '',
      reason: '',
    },
    validate: {
      email: (value) => (/^\S+@\S+$/.test(value) ? null : 'Invalid email'),
    },
  });

  const filteredSuppressions = suppressions.filter(s => 
    s.email.toLowerCase().includes(search.toLowerCase()) || 
    s.reason.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Group justify="space-between" align="flex-end">
          <Box>
            <Group gap="xs" mb={4}>
              <ThemeIcon variant="light" color="red" size="md">
                <IconShieldCancel size={18} />
              </ThemeIcon>
              <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Suppressions</Title>
            </Group>
            <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Manage your global "Do Not Send" list to protect your sender reputation.</Text>
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
              color="red"
              radius="md"
              size="sm"
            >
              Add Suppression
            </Button>
          </Group>
        </Group>

        <TextInput
          placeholder="Search by email or reason..."
          leftSection={<IconSearch size={16} />}
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
          radius="md"
        />

        <Box style={{ 
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))', 
          borderRadius: rem(12), 
          border: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))', 
          overflow: 'hidden' 
        }}>
          <Table verticalSpacing="md" horizontalSpacing="lg">
            <Table.Thead style={{ backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))' }}>
              <Table.Tr>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Email Address</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Reason</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Added At</Text></Table.Th>
                <Table.Th style={{ textAlign: 'right' }}><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Actions</Text></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {filteredSuppressions.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={4} style={{ textAlign: 'center', padding: rem(40) }}>
                    <Text c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={500}>No suppressions found.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                filteredSuppressions.map((s) => (
                  <Table.Tr key={s.id} style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
                    <Table.Td>
                      <Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{s.email}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))">{s.reason || 'No reason provided'}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">
                        {s.createTime ? new Date(Number(s.createTime.seconds) * 1000).toLocaleDateString() : '-'}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Group justify="flex-end">
                        <ActionIcon variant="light" color="red" onClick={() => {
                          if (window.confirm(`Remove ${s.email} from suppression list?`)) {
                            deleteMutation.mutate(s.email);
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
        onClose={() => setIsModalOpen(false)}
        title={<Text fw={800} size="lg">Add to Suppression List</Text>}
        radius="md"
      >
        <form onSubmit={form.onSubmit((values) => addMutation.mutate(values))}>
          <Stack gap="md">
            <TextInput
              label="Email Address"
              placeholder="customer@example.com"
              required
              {...form.getInputProps('email')}
            />
            <Textarea
              label="Reason"
              placeholder="e.g. Repeated bounces, User unsubscribed"
              {...form.getInputProps('reason')}
            />
            <Group justify="flex-end" mt="xl">
              <Button type="submit" loading={addMutation.isPending} color="red" radius="md">
                Add to List
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Container>
  );
};
