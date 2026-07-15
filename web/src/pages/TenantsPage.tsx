import React, { useState, useEffect } from 'react';
import { Container, Title, Group, Stack, Box, Text, rem, ThemeIcon, Table, ActionIcon, Menu, Button, Modal, TextInput, Select } from '@mantine/core';
import { IconBuildingCommunity, IconDotsVertical, IconTrash, IconPlus, IconExternalLink, IconCheck, IconEdit, IconRefresh, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { tenantService } from '../services/tenant';
import { useDisclosure } from '@mantine/hooks';
import { useForm } from '@mantine/form';
import { useAuthStore } from '../store/authStore';
import { notifications } from '@mantine/notifications';
import { TagsInput } from '@mantine/core';

export const TenantsPage: React.FC = () => {
  const { setSelectedTenantID } = useAuthStore();
  const [opened, { open, close }] = useDisclosure(false);
  const [editingTenant, setEditingTenant] = useState<any>(null);
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('10');
  const queryClient = useQueryClient();

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const { data, isLoading } = useQuery({
    queryKey: ['tenants', pageToken, pageSize],
    queryFn: () => tenantService.listTenants(Number(pageSize), pageToken),
  });

  const form = useForm({
    initialValues: { 
      name: '',
      retryPattern: [] as string[]
    },
    validate: {
      name: (value) => (value.length < 2 ? 'Name must be at least 2 characters' : null),
    },
  });

  const createMutation = useMutation({
    mutationFn: (values: { name: string, retryPattern: string[] }) => 
      tenantService.createTenant(values.name, values.retryPattern),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tenants'] });
      notifications.show({
        title: 'Success',
        message: 'Tenant created successfully',
        color: 'green',
        icon: <IconCheck size={16} />,
      });
      form.reset();
      close();
    },
    onError: (error: any) => {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to create tenant',
        color: 'red',
      });
    },
  });

  const updateMutation = useMutation({
    mutationFn: (values: { id: string, name: string, retryPattern: string[] }) => 
      tenantService.updateTenant(values.id, values.name, values.retryPattern),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tenants'] });
      notifications.show({
        title: 'Success',
        message: 'Tenant updated successfully',
        color: 'green',
        icon: <IconCheck size={16} />,
      });
      form.reset();
      setEditingTenant(null);
      close();
    },
    onError: (error: any) => {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to update tenant',
        color: 'red',
      });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => tenantService.deleteTenant(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tenants'] });
      notifications.show({
        title: 'Success',
        message: 'Tenant deleted successfully',
        color: 'green',
      });
    },
    onError: (error: any) => {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to delete tenant',
        color: 'red',
      });
    },
  });

  const tenants = data?.tenants || [];
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

  const handleOpenCreate = () => {
    setEditingTenant(null);
    form.reset();
    open();
  };

  const handleEdit = (tenant: any) => {
    setEditingTenant(tenant);
    form.setValues({
      name: tenant.name,
      retryPattern: tenant.retryPattern || []
    });
    open();
  };

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Group justify="space-between">
          <Box>
            <Group gap="xs" mb={4}>
              <ThemeIcon variant="light" color="brand" size="md">
                <IconBuildingCommunity size={18} />
              </ThemeIcon>
              <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Tenant Management</Title>
            </Group>
            <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Global management of all tenants in the system.</Text>
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
            <Button leftSection={<IconPlus size={16} />} color="brand" onClick={handleOpenCreate}>Add Tenant</Button>
          </Group>
        </Group>

        <Modal opened={opened} onClose={close} title={editingTenant ? "Edit Tenant" : "Create New Tenant"} centered radius="md">
          <form onSubmit={form.onSubmit((values) => {
            if (editingTenant) {
              updateMutation.mutate({ id: editingTenant.id, ...values });
            } else {
              createMutation.mutate(values);
            }
          })}>
            <Stack>
              <TextInput
                label="Tenant Name"
                placeholder="e.g. Acme Corp"
                required
                {...form.getInputProps('name')}
              />
              <TagsInput
                label="Retry Pattern"
                placeholder="e.g. 5m, 1h, 1d"
                description="Custom retry intervals for this tenant (overrides global settings). Units: m, h, d."
                leftSection={<IconRefresh size={14} />}
                {...form.getInputProps('retryPattern')}
              />
              <Group justify="flex-end" mt="md">
                <Button variant="subtle" onClick={close} color="gray">Cancel</Button>
                <Button type="submit" color="brand" loading={createMutation.isPending || updateMutation.isPending}>
                  {editingTenant ? "Save Changes" : "Create Tenant"}
                </Button>
              </Group>
            </Stack>
          </form>
        </Modal>

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
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">ID</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Created At</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Actions</Text></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {tenants.map((t) => (
                <Table.Tr key={t.id} style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
                  <Table.Td>
                    <Text size="sm" fw={700} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{t.name}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" style={{ fontFamily: 'monospace' }}>{t.id}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))">{t.createdAt}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Group gap="xs">
                        <Button
                            variant="light"
                            size="compact-xs"
                            leftSection={<IconExternalLink size={12}/>}
                            onClick={() => setSelectedTenantID(t.id)}
                            color="brand"
                        >
                            Switch To
                        </Button>
                        <Menu position="bottom-end" shadow="md">
                            <Menu.Target>
                            <ActionIcon variant="subtle" color="gray">
                                <IconDotsVertical size={16} />
                            </ActionIcon>
                            </Menu.Target>
                            <Menu.Dropdown>
                            <Menu.Item leftSection={<IconEdit size={14} />} onClick={() => handleEdit(t)}>Edit Tenant</Menu.Item>
                            <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={() => {
                                if (window.confirm(`Are you sure you want to delete tenant "${t.name}"?`)) {
                                    deleteMutation.mutate(t.id);
                                }
                            }}>Delete Tenant</Menu.Item>
                            </Menu.Dropdown>
                        </Menu>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
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
    </Container>
  );
};
