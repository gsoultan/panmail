import React, { useMemo, useState } from 'react';
import {
  Alert, Badge, Button, Group, Modal, Select, Stack, Table, Text, ActionIcon, Tooltip, Loader, Center,
} from '@mantine/core';
import { IconBuildingCommunity, IconTrash, IconPlus, IconHome } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { notifications } from '@mantine/notifications';
import { UserRole } from '../../../api/panmail/v1/auth_pb';
import { userService } from '../../../services/user';
import { tenantService } from '../../../services/tenant';
import { ASSIGNABLE_ROLES, MEMBERSHIP_ROLE_LABELS } from './membershipRoles';

/**
 * Assigning an existing account to another tenant, rather than creating a
 * second account for the same person.
 *
 * Two rules from the server are surfaced here rather than left to a failed
 * request: the home tenant cannot be revoked, and super admin is not a role a
 * membership can carry — it is global, and granting it per tenant would hand
 * out authority over every other one.
 */

export interface UserTenantsModalProps {
  opened: boolean;
  onClose: () => void;
  user: { id: string; name: string; email: string } | null;
}

export const UserTenantsModal: React.FC<UserTenantsModalProps> = ({ opened, onClose, user }) => {
  const queryClient = useQueryClient();
  const [tenantId, setTenantId] = useState<string | null>(null);
  const [role, setRole] = useState<string | null>(UserRole.VIEWER.toString());

  const { data: memberships, isLoading } = useQuery({
    queryKey: ['user-tenants', user?.id],
    queryFn: () => userService.listUserTenants(user!.id),
    enabled: opened && !!user,
  });

  const { data: allTenants } = useQuery({
    queryKey: ['tenants', 'all'],
    queryFn: () => tenantService.listTenants(100),
    enabled: opened,
  });

  const current = memberships?.tenants ?? [];

  // Only tenants the user is not already in are offerable; re-assigning an
  // existing membership is a role change, which the row's own control does.
  const available = useMemo(() => {
    const held = new Set((memberships?.tenants ?? []).map((m) => m.tenantId));
    return (allTenants?.tenants ?? [])
      .filter((t) => !held.has(t.id))
      .map((t) => ({ value: t.id, label: t.name }));
  }, [allTenants, memberships]);

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['user-tenants', user?.id] });
    queryClient.invalidateQueries({ queryKey: ['users'] });
  };

  const fail = (error: unknown, fallback: string) => {
    notifications.show({
      title: 'Error',
      message: error instanceof Error ? error.message : fallback,
      color: 'red',
    });
  };

  const assign = useMutation({
    mutationFn: ({ tenant, newRole }: { tenant: string; newRole: UserRole }) =>
      userService.assignUserToTenant(user!.id, tenant, newRole),
    onSuccess: () => {
      refresh();
      setTenantId(null);
      notifications.show({ title: 'Assigned', message: `${user?.name} can now act in that tenant.`, color: 'green' });
    },
    onError: (error) => fail(error, 'Could not assign the user to that tenant'),
  });

  const remove = useMutation({
    mutationFn: (tenant: string) => userService.removeUserFromTenant(user!.id, tenant),
    onSuccess: () => {
      refresh();
      notifications.show({ title: 'Removed', message: 'The membership was revoked.', color: 'green' });
    },
    onError: (error) => fail(error, 'Could not remove the membership'),
  });

  return (
    <Modal opened={opened} onClose={onClose} title="Tenant Access" centered radius="md" size="lg">
      <Stack>
        <Text size="sm">
          Tenants <b>{user?.name}</b> ({user?.email}) may act in. One account, one password —
          assigning here replaces creating a second user for the same person.
        </Text>

        {isLoading ? (
          <Center py="lg"><Loader size="sm" /></Center>
        ) : (
          <Table verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Tenant</Table.Th>
                <Table.Th>Role there</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {current.map((m) => {
                const label = MEMBERSHIP_ROLE_LABELS[m.role as UserRole] ?? MEMBERSHIP_ROLE_LABELS[UserRole.VIEWER]!;
                return (
                  <Table.Tr key={m.tenantId}>
                    <Table.Td>
                      <Group gap="xs">
                        <Text size="sm" fw={600}>{m.tenantName}</Text>
                        {m.isHome && (
                          <Tooltip label="Signs in here. Delete the account to remove this.">
                            <Badge size="xs" variant="light" color="brand" leftSection={<IconHome size={10} />}>Home</Badge>
                          </Tooltip>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      {m.isHome || m.role === UserRole.SUPER_ADMIN ? (
                        <Badge variant="light" color={label.color} radius="sm">{label.label}</Badge>
                      ) : (
                        <Select
                          size="xs"
                          data={ASSIGNABLE_ROLES}
                          value={m.role.toString()}
                          allowDeselect={false}
                          disabled={assign.isPending}
                          onChange={(value) =>
                            value && assign.mutate({ tenant: m.tenantId, newRole: Number(value) as UserRole })
                          }
                          style={{ width: 150 }}
                        />
                      )}
                    </Table.Td>
                    <Table.Td>
                      {!m.isHome && (
                        <Tooltip label="Revoke access to this tenant">
                          <ActionIcon
                            variant="subtle"
                            color="red"
                            disabled={remove.isPending}
                            onClick={() => remove.mutate(m.tenantId)}
                          >
                            <IconTrash size={16} />
                          </ActionIcon>
                        </Tooltip>
                      )}
                    </Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
        )}

        {available.length === 0 ? (
          <Alert variant="light" color="gray" icon={<IconBuildingCommunity size={16} />}>
            This user already has access to every tenant.
          </Alert>
        ) : (
          <Group align="flex-end" gap="sm">
            <Select
              label="Assign to tenant"
              placeholder="Pick a tenant"
              data={available}
              value={tenantId}
              onChange={setTenantId}
              searchable
              style={{ flex: 1 }}
            />
            <Select
              label="Role"
              data={ASSIGNABLE_ROLES}
              value={role}
              onChange={setRole}
              allowDeselect={false}
              style={{ width: 160 }}
            />
            <Button
              leftSection={<IconPlus size={16} />}
              color="brand"
              disabled={!tenantId}
              loading={assign.isPending}
              onClick={() => tenantId && assign.mutate({ tenant: tenantId, newRole: Number(role) as UserRole })}
            >
              Assign
            </Button>
          </Group>
        )}
      </Stack>
    </Modal>
  );
};
