import React, { useState, useEffect } from 'react';
import { Container, Title, Group, Stack, Box, Text, rem, ThemeIcon, Table, Badge, ActionIcon, Menu, Avatar, Button, Modal, TextInput, PasswordInput, Select, Switch } from '@mantine/core';
import { IconUsers, IconUserCircle, IconDotsVertical, IconTrash, IconEdit, IconShieldLock, IconPlus, IconCheck, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { userClient } from '../services/client';
import { UserRole } from '../api/panmail/v1/auth_pb';
import { useDisclosure } from '@mantine/hooks';
import { useForm } from '@mantine/form';
import { notifications } from '@mantine/notifications';
import { useAuthStore } from '../store/authStore';

const roleConfig: Record<UserRole, { label: string, color: string }> = {
  [UserRole.UNSPECIFIED]: { label: 'Unspecified', color: 'gray' },
  [UserRole.SUPER_ADMIN]: { label: 'Super Admin', color: 'red' },
  [UserRole.ADMIN]: { label: 'Administrator', color: 'blue' },
  [UserRole.EDITOR]: { label: 'Editor', color: 'green' },
  [UserRole.VIEWER]: { label: 'Viewer', color: 'gray' },
};

export const UsersPage: React.FC = () => {
  const { user } = useAuthStore();
  const [createOpened, { open: openCreate, close: closeCreate }] = useDisclosure(false);
  const [roleOpened, { open: openRole, close: closeRole }] = useDisclosure(false);
  const [selectedUser, setSelectedUser] = useState<any>(null);
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('10');
  const queryClient = useQueryClient();

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const { data, isLoading } = useQuery({
    queryKey: ['users', pageToken, pageSize],
    queryFn: () => userClient.listUsers({ pageSize: Number(pageSize), pageToken }),
  });

  const users = data?.users || [];
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

  const createForm = useForm({
    initialValues: {
      email: '',
      password: '',
      name: '',
      role: UserRole.VIEWER,
    },
    validate: {
      email: (value) => (/^\S+@\S+$/.test(value) ? null : 'Invalid email'),
      password: (value) => (value.length < 6 ? 'Password must be at least 6 characters' : null),
      name: (value) => (value.length < 2 ? 'Name is too short' : null),
    },
  });

  const roleForm = useForm({
    initialValues: {
      role: UserRole.VIEWER,
    },
  });

  const createMutation = useMutation({
    mutationFn: (values: typeof createForm.values) => userClient.createUser({
      ...values,
      role: Number(values.role)
    }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] });
      notifications.show({
        title: 'Success',
        message: 'User created successfully',
        color: 'green',
        icon: <IconCheck size={16} />,
      });
      createForm.reset();
      closeCreate();
    },
    onError: (error: any) => {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to create user',
        color: 'red',
      });
    },
  });

  const updateRoleMutation = useMutation({
    mutationFn: (values: { id: string, role: UserRole }) => userClient.updateUserRole(values),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] });
      notifications.show({
        title: 'Success',
        message: 'User role updated',
        color: 'green',
      });
      closeRole();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => userClient.deleteUser({ id }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] });
      notifications.show({
        title: 'Success',
        message: 'User deleted successfully',
        color: 'green',
      });
    },
  });

  const update2FAMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => userClient.updateUserTwoFactor({ id, enabled }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] });
      notifications.show({
        title: 'Success',
        message: 'Two-factor authentication updated',
        color: 'green',
      });
    },
    onError: (error: any) => {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to update 2FA',
        color: 'red',
      });
    }
  });

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Group justify="space-between">
          <Box>
            <Group gap="xs" mb={4}>
              <ThemeIcon variant="light" color="brand" size="md">
                <IconUsers size={18} />
              </ThemeIcon>
              <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>User Management</Title>
            </Group>
            <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Manage users and permissions for the current tenant.</Text>
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
            <Button leftSection={<IconPlus size={16} />} color="brand" onClick={openCreate}>Add User</Button>
          </Group>
        </Group>

        <Modal opened={createOpened} onClose={closeCreate} title="Create New User" centered radius="md">
          <form onSubmit={createForm.onSubmit((values) => createMutation.mutate(values))}>
            <Stack>
              <TextInput label="Name" placeholder="Full Name" required {...createForm.getInputProps('name')} />
              <TextInput label="Email" placeholder="user@example.com" required {...createForm.getInputProps('email')} />
              <PasswordInput label="Password" placeholder="Secure password" required {...createForm.getInputProps('password')} />
              <Select
                label="Role"
                data={[
                  ...(user?.role === UserRole.SUPER_ADMIN ? [{ value: UserRole.SUPER_ADMIN.toString(), label: 'Super Admin' }] : []),
                  { value: UserRole.ADMIN.toString(), label: 'Administrator' },
                  { value: UserRole.EDITOR.toString(), label: 'Editor' },
                  { value: UserRole.VIEWER.toString(), label: 'Viewer' },
                ]}
                {...createForm.getInputProps('role')}
              />
              <Group justify="flex-end" mt="md">
                <Button variant="subtle" onClick={closeCreate} color="gray">Cancel</Button>
                <Button type="submit" color="brand" loading={createMutation.isPending}>Create User</Button>
              </Group>
            </Stack>
          </form>
        </Modal>

        <Modal opened={roleOpened} onClose={closeRole} title="Update User Role" centered radius="md">
          <form onSubmit={roleForm.onSubmit((values) => updateRoleMutation.mutate({ id: selectedUser?.id, role: Number(values.role) }))}>
            <Stack>
              <Text size="sm">Update role for <b>{selectedUser?.name}</b></Text>
              <Select
                label="New Role"
                data={[
                  ...(user?.role === UserRole.SUPER_ADMIN ? [{ value: UserRole.SUPER_ADMIN.toString(), label: 'Super Admin' }] : []),
                  { value: UserRole.ADMIN.toString(), label: 'Administrator' },
                  { value: UserRole.EDITOR.toString(), label: 'Editor' },
                  { value: UserRole.VIEWER.toString(), label: 'Viewer' },
                ]}
                {...roleForm.getInputProps('role')}
              />
              <Group justify="flex-end" mt="md">
                <Button variant="subtle" onClick={closeRole} color="gray">Cancel</Button>
                <Button type="submit" color="brand" loading={updateRoleMutation.isPending}>Update Role</Button>
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
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">User</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Role</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Two-Factor</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Actions</Text></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {users.map((u: any) => {
                const role = roleConfig[u.role as UserRole] || roleConfig[UserRole.VIEWER];
                return (
                  <Table.Tr key={u.id} style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
                    <Table.Td>
                      <Group gap="sm">
                        <Avatar size="sm" radius="xl" color="brand">
                          <IconUserCircle size={16} />
                        </Avatar>
                        <div>
                          <Text size="sm" fw={700} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{u.name}</Text>
                          <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">{u.email}</Text>
                        </div>
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={role.color} radius="sm">
                        {role.label}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Switch
                        checked={u.twoFactorEnabled}
                        onChange={(e: React.ChangeEvent<HTMLInputElement>) => update2FAMutation.mutate({ id: u.id, enabled: e.currentTarget.checked })}
                        color="green"
                        size="sm"
                        disabled={update2FAMutation.isPending}
                      />
                    </Table.Td>
                    <Table.Td>
                      <Menu position="bottom-end" shadow="md">
                        <Menu.Target>
                          <ActionIcon variant="subtle" color="gray">
                            <IconDotsVertical size={16} />
                          </ActionIcon>
                        </Menu.Target>
                        <Menu.Dropdown>
                          <Menu.Item leftSection={<IconShieldLock size={14} />} onClick={() => {
                            setSelectedUser(u);
                            roleForm.setValues({ role: u.role });
                            openRole();
                          }}>Change Role</Menu.Item>
                          <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={() => {
                            if (window.confirm(`Are you sure you want to delete user "${u.name}"?`)) {
                              deleteMutation.mutate(u.id);
                            }
                          }}>Delete User</Menu.Item>
                        </Menu.Dropdown>
                      </Menu>
                    </Table.Td>
                  </Table.Tr>
                );
              })}
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
