import React, { useState, useEffect } from 'react';
import { 
  Container, 
  Title, 
  Group, 
  Stack, 
  Box, 
  Text, 
  rem, 
  ThemeIcon, 
  Table, 
  Button, 
  Modal, 
  TextInput, 
  ActionIcon, 
  Code,
  Alert,
  Paper,
  Select
} from '@mantine/core';
import { IconKey, IconPlus, IconTrash, IconCopy, IconCheck, IconAlertCircle, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiKeyService } from '../features/auth/services/apiKey';
import { notifications } from '@mantine/notifications';

export const ApiKeysPage: React.FC = () => {
  const queryClient = useQueryClient();
  const [createModalOpened, setCreateModalOpened] = useState(false);
  const [keyName, setKeyName] = useState('');
  const [newKey, setNewKey] = useState<{ name: string; key: string } | null>(null);
  const [copied, setCopied] = useState(false);

  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('10');

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const { data, isLoading } = useQuery({
    queryKey: ['apiKeys', pageToken, pageSize],
    queryFn: () => apiKeyService.listApiKeys(Number(pageSize), pageToken),
  });

  const createMutation = useMutation({
    mutationFn: (name: string) => apiKeyService.createApiKey(name),
    onSuccess: (res) => {
      setNewKey({ name: res.apiKey?.name || '', key: res.plainTextKey });
      setKeyName('');
      queryClient.invalidateQueries({ queryKey: ['apiKeys'] });
    },
    onError: () => {
      notifications.show({
        title: 'Error',
        message: 'Failed to create API key',
        color: 'red',
      });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => apiKeyService.deleteApiKey(id),
    onSuccess: () => {
      notifications.show({
        title: 'Success',
        message: 'API key deleted',
        color: 'green',
      });
      queryClient.invalidateQueries({ queryKey: ['apiKeys'] });
    },
  });

  const handleCreate = () => {
    if (!keyName.trim()) return;
    createMutation.mutate(keyName);
  };

  const handleCopy = () => {
    if (newKey) {
      navigator.clipboard.writeText(newKey.key);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
      notifications.show({
        message: 'API key copied to clipboard',
        color: 'brand',
      });
    }
  };

  const keys = data?.apiKeys || [];
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

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Group justify="space-between">
          <Box>
            <Group gap="xs" mb={4}>
              <ThemeIcon variant="light" color="brand" size="md">
                <IconKey size={18} />
              </ThemeIcon>
              <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>API Keys</Title>
            </Group>
            <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Manage safety API keys for integrating other applications with Panmail.</Text>
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
              leftSection={<IconPlus size={16} />}
              onClick={() => {
                setNewKey(null);
                setCreateModalOpened(true);
              }}
              color="brand"
            >
              Create API Key
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
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Prefix</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Created At</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Last Used</Text></Table.Th>
                <Table.Th style={{ textAlign: 'right' }}><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Actions</Text></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {keys.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={5} style={{ textAlign: 'center', padding: rem(40) }}>
                    <ThemeIcon variant="light" color="gray" size={60} radius="xl" mb="md">
                       <IconKey size={30} />
                    </ThemeIcon>
                    <Text c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={500}>No API keys found. Create one to start integrating.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                keys.map((key) => (
                  <Table.Tr key={key.id} style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
                    <Table.Td>
                      <Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{key.name}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Code fw={700} bg="light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-7))">{key.prefix}...</Code>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))">{new Date(key.createdAt).toLocaleString()}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))">
                        {key.lastUsedAt ? new Date(key.lastUsedAt).toLocaleString() : 'Never'}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Group justify="flex-end">
                        <ActionIcon variant="light" color="red" onClick={() => deleteMutation.mutate(key.id)}>
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
        opened={createModalOpened}
        onClose={() => setCreateModalOpened(false)}
        title={<Text fw={800} size="lg">Create API Key</Text>}
        radius="md"
      >
        {!newKey ? (
          <Stack gap="md">
            <TextInput
              label="Key Name"
              placeholder="e.g. Production Backend"
              value={keyName}
              onChange={(e) => setKeyName(e.currentTarget.value)}
              required
            />
            <Button fullWidth onClick={handleCreate} loading={createMutation.isPending}>
              Generate Key
            </Button>
          </Stack>
        ) : (
          <Stack gap="md">
            <Alert icon={<IconAlertCircle size={16} />} title="Safety Warning" color="indigo" radius="md">
              Please copy your API key now. You won't be able to see it again for security reasons.
            </Alert>
            
            <Box>
              <Text size="sm" fw={700} mb={8} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Your API Key:</Text>
              <Group gap={0} wrap="nowrap">
                <Paper withBorder p="xs" style={{ flexGrow: 1, backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))', borderTopRightRadius: 0, borderBottomRightRadius: 0 }}>
                  <Text size="sm" fw={700} style={{ wordBreak: 'break-all', fontFamily: 'monospace' }} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">
                    {newKey.key}
                  </Text>
                </Paper>
                <Button
                  onClick={handleCopy}
                  variant="filled"
                  color={copied ? 'green' : 'brand'}
                  style={{ borderTopLeftRadius: 0, borderBottomLeftRadius: 0, height: 'auto' }}
                  px="sm"
                >
                  {copied ? <IconCheck size={18} /> : <IconCopy size={18} />}
                </Button>
              </Group>
            </Box>

            <Button fullWidth variant="light" onClick={() => setCreateModalOpened(false)}>
              Done
            </Button>
          </Stack>
        )}
      </Modal>
    </Container>
  );
};
