import React from 'react';
import {
  AppShell,
  Burger,
  Group,
  Title,
  UnstyledButton,
  Stack,
  Text,
  Menu,
  Avatar,
  rem,
  ThemeIcon,
  ActionIcon,
  useMantineColorScheme,
  useComputedColorScheme,
  Box,
  Tooltip,
  ScrollArea
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import {
  IconMail,
  IconLogout,
  IconUser,
  IconSettings,
  IconHistory,
  IconArchive,
  IconSun,
  IconMoon,
  IconLayoutDashboard,
  IconChevronRight,
  IconTemplate,
  IconChartBar,
  IconShieldCancel,
  IconDownload,
  IconKey,
  IconWebhook,
  IconBuildingCommunity,
  IconUsers,
  IconSend
} from '@tabler/icons-react';
import { Outlet, useNavigate, Link, useLocation } from '@tanstack/react-router';
import { useAuthStore } from '../store/authStore';
import { Select } from '@mantine/core';
import { useQuery } from '@tanstack/react-query';
import { tenantService } from '../services/tenant';
import { UserRole } from '../api/panmail/v1/auth_pb';
import classes from './AppLayout.module.css';

interface NavItemProps {
  icon: React.FC<any>;
  label: string;
  to: string;
  active?: boolean;
}

export const AppLayout: React.FC = () => {
  const [opened, { toggle }] = useDisclosure();
  const [collapsed, { toggle: toggleCollapsed }] = useDisclosure(false);
  const navigate = useNavigate();
  const location = useLocation();
  const { user, clearAuth, selectedTenantID, setSelectedTenantID } = useAuthStore();
  const { setColorScheme } = useMantineColorScheme();
  const computedColorScheme = useComputedColorScheme('light', { getInitialValueInEffect: true });

  const isSuperAdmin = user?.role === UserRole.SUPER_ADMIN;

  const { data: tenantsData } = useQuery({
    queryKey: ['tenants'],
    queryFn: () => tenantService.listTenants(),
    enabled: isSuperAdmin,
  });

  const tenants = tenantsData?.tenants || [];
  const tenantOptions = tenants.map(t => ({ value: t.id, label: t.name }));

  const handleSignOut = () => {
    clearAuth();
    navigate({ to: '/signin' });
  };

  const toggleColorScheme = () => {
    setColorScheme(computedColorScheme === 'dark' ? 'light' : 'dark');
  };

  const userInitials = user?.name ? user.name.split(' ').map(n => n[0]).join('').toUpperCase() : 'AD';

  const NavItem = ({ icon: Icon, label, to, active }: NavItemProps) => {
    const content = (
      <UnstyledButton
        component={Link}
        to={to}
        className={`${classes.navItem} ${active ? classes.navItemActive : classes.navItemInactive}`}
        style={{
          padding: collapsed ? rem(12) : `${rem(10)} ${rem(16)}`,
        }}
      >
        <Group justify={collapsed ? "center" : "space-between"} gap={collapsed ? 0 : "sm"}>
          <Group gap="sm">
            <Icon size={20} stroke={1.5} />
            {!collapsed && (
              <Text size="sm" fw={active ? 700 : 500} c="inherit">
                {label}
              </Text>
            )}
          </Group>
          {!collapsed && active && <IconChevronRight size={14} stroke={2} />}
        </Group>
      </UnstyledButton>
    );

    if (collapsed) {
      return (
        <Tooltip label={label} position="right" withArrow offset={10}>
          {content}
        </Tooltip>
      );
    }

    return content;
  };

  return (
    <AppShell
      header={{ height: 64 }}
      navbar={{
        width: collapsed ? 80 : 280,
        breakpoint: 'sm',
        collapsed: { mobile: !opened }
      }}
      padding="xl"
    >
      <AppShell.Header>
        <Group h="100%" px="xl" justify="space-between">
          <Group gap="lg">
            <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" />
            <Burger opened={!collapsed} onClick={toggleCollapsed} visibleFrom="sm" size="sm" />
            <Group gap="xs">
              <ThemeIcon size={38} radius="md" variant="gradient" gradient={{ from: 'brand.6', to: 'brand.4' }}>
                <IconMail size={24} />
              </ThemeIcon>
              {!collapsed && (
                <Title order={2} style={{ letterSpacing: rem(-1), fontWeight: 900, fontSize: rem(22) }}>
                  Panmail
                </Title>
              )}
            </Group>
          </Group>

          <Group gap="md">
            {isSuperAdmin && tenantOptions.length > 0 && (
              <Select
                placeholder="Switch Tenant"
                data={tenantOptions}
                value={selectedTenantID}
                onChange={setSelectedTenantID}
                size="sm"
                radius="md"
                leftSection={<IconBuildingCommunity size={16} />}
                style={{ width: 220 }}
              />
            )}

            <Tooltip label={computedColorScheme === 'dark' ? 'Light mode' : 'Dark mode'}>
              <ActionIcon
                onClick={toggleColorScheme}
                variant="default"
                size="lg"
                aria-label="Toggle color scheme"
                radius="md"
              >
                {computedColorScheme === 'dark' ? (
                  <IconSun size={20} stroke={1.5} />
                ) : (
                  <IconMoon size={20} stroke={1.5} />
                )}
              </ActionIcon>
            </Tooltip>

            <Menu shadow="lg" width={220} radius="md" transitionProps={{ transition: 'pop-top-right' }}>
              <Menu.Target>
                <UnstyledButton className={classes.profileButton}>
                  <Group gap="sm" px={rem(4)} py={rem(4)}>
                    <Avatar color="brand" radius="md" size="md" fw={700}>{userInitials}</Avatar>
                    <Box visibleFrom="xs">
                      <Text size="sm" fw={700} style={{ lineHeight: 1 }} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{user?.name || 'Admin'}</Text>
                      <Text c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-1))" size="xs" fw={500} mt={4}>{user?.email || 'admin@panmail.dev'}</Text>
                    </Box>
                  </Group>
                </UnstyledButton>
              </Menu.Target>

              <Menu.Dropdown>
                <Menu.Label>User Account</Menu.Label>
                <Menu.Item
                  leftSection={<IconUser style={{ width: rem(16), height: rem(16) }} stroke={1.5} />}
                  onClick={() => navigate({ to: '/profile' })}
                >
                  My Profile
                </Menu.Item>
                <Menu.Item
                  leftSection={<IconSettings style={{ width: rem(16), height: rem(16) }} stroke={1.5} />}
                  onClick={() => navigate({ to: '/settings' })}
                >
                  Settings
                </Menu.Item>

                <Menu.Divider />

                <Menu.Label>Danger Zone</Menu.Label>
                <Menu.Item
                  color="red"
                  leftSection={<IconLogout style={{ width: rem(16), height: rem(16) }} stroke={1.5} />}
                  onClick={handleSignOut}
                >
                  Sign Out
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p={0}>
        <AppShell.Section grow style={{ overflow: 'hidden' }}>
          <ScrollArea h="100%" offsetScrollbars scrollbarSize={4}>
            <Stack gap="xs" p="md">
              {!collapsed && (
                <Text size="xs" fw={700} c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" tt="uppercase" px="md" mb={4}>
                  General
                </Text>
              )}
              <NavItem
                icon={IconLayoutDashboard}
                label="Dashboard"
                to="/dashboard"
                active={location.pathname === '/dashboard'}
              />
              <NavItem
                icon={IconChartBar}
                label="Analytics"
                to="/analytics"
                active={location.pathname === '/analytics'}
              />
              <NavItem
                icon={IconArchive}
                label="Archives"
                to="/archives"
                active={location.pathname === '/archives'}
              />

              {!collapsed && (
                <Text size="xs" fw={700} c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" tt="uppercase" px="md" mb={4} mt="md">
                  Email Gateway
                </Text>
              )}
              <NavItem
                icon={IconMail}
                label="Providers"
                to="/"
                active={location.pathname === '/'}
              />
              <NavItem
                icon={IconTemplate}
                label="Templates"
                to="/templates"
                active={location.pathname === '/templates'}
              />
              <NavItem
                icon={IconDownload}
                label="Inbound"
                to="/inbound"
                active={location.pathname === '/inbound'}
              />
              <NavItem
                icon={IconShieldCancel}
                label="Suppressions"
                to="/suppressions"
                active={location.pathname === '/suppressions'}
              />
              <NavItem
                icon={IconHistory}
                label="Activity Logs"
                to="/logs"
                active={location.pathname === '/logs'}
              />

              {!collapsed && (
                <Text size="xs" fw={700} c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" tt="uppercase" px="md" mb={4} mt="md">
                  Automation
                </Text>
              )}
              <NavItem
                icon={IconWebhook}
                label="Webhooks"
                to="/webhooks"
                active={location.pathname === '/webhooks'}
              />

              {!collapsed && (
                <Text size="xs" fw={700} c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" tt="uppercase" px="md" mb={4} mt="md">
                  Test
                </Text>
              )}
              <NavItem
                icon={IconSend}
                label="Delivery"
                to="/test-delivery"
                active={location.pathname === '/test-delivery'}
              />

              {(user?.role === UserRole.SUPER_ADMIN || user?.role === UserRole.ADMIN) && (
                <>
                  {!collapsed && (
                    <Text size="xs" fw={700} c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" tt="uppercase" px="md" mb={4} mt="md">
                      Administration
                    </Text>
                  )}
                  <NavItem
                    icon={IconKey}
                    label="API Keys"
                    to="/api-keys"
                    active={location.pathname === '/api-keys'}
                  />
                  <NavItem
                    icon={IconUsers}
                    label="Users"
                    to="/users"
                    active={location.pathname === '/users'}
                  />
                  <NavItem
                    icon={IconSettings}
                    label="Settings"
                    to="/settings"
                    active={location.pathname === '/settings'}
                  />
                </>
              )}

              {user?.role === UserRole.SUPER_ADMIN && (
                <NavItem
                  icon={IconBuildingCommunity}
                  label="Tenants"
                  to="/tenants"
                  active={location.pathname === '/tenants'}
                />
              )}
            </Stack>
          </ScrollArea>
        </AppShell.Section>

        <AppShell.Section p="md">
          <Box style={{ borderTop: `1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))`, paddingTop: rem(12) }}>
            <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" ta="center" fw={600}>
              Panmail Gateway v{import.meta.env.VITE_APP_VERSION || '1.0.0'}
            </Text>
          </Box>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main>
        <Box style={{ maxWidth: rem(1200), margin: '0 auto' }}>
          <Outlet />
        </Box>
      </AppShell.Main>
    </AppShell>
  );
};
