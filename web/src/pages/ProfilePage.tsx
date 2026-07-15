import React, { useState } from 'react';
import {
  Container,
  Title,
  Text,
  Stack,
  Group,
  Paper,
  Avatar,
  Box,
  rem,
  ThemeIcon,
  Divider,
  Grid,
  TextInput,
  Button,
  Modal,
  Alert,
  Center,
  Loader
} from '@mantine/core';
import { IconUser, IconShieldLock, IconMail, IconQrcode, IconDeviceMobile, IconAlertCircle, IconCheck } from '@tabler/icons-react';
import { useAuthStore } from '../store/authStore';
import { authClient } from '../services/client';
import { notifications } from '@mantine/notifications';
import { QRCodeSVG } from 'qrcode.react';

export const ProfilePage: React.FC = () => {
  const { user } = useAuthStore();
  const [twoFactorModalOpened, setTwoFactorModalOpened] = useState(false);
  const [setupLoading, setSetupLoading] = useState(false);
  const [setupData, setSetupLoadingData] = useState<{ secret: string; qrCodeUrl: string } | null>(null);
  const [verificationCode, setVerificationCode] = useState('');
  const [verifying, setVerifying] = useState(false);

  const userInitials = user?.name ? user.name.split(' ').map(n => n[0]).join('').toUpperCase() : 'AD';

  const handleSetup2FA = async () => {
    setSetupLoading(true);
    try {
      const res = await authClient.setupTwoFactor({});
      setSetupLoadingData({ secret: res.secret, qrCodeUrl: res.qrCodeUrl });
      setTwoFactorModalOpened(true);
    } catch (error: any) {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to initialize 2FA setup',
        color: 'red',
      });
    } finally {
      setSetupLoading(false);
    }
  };

  const handleVerifyAndEnable = async () => {
    if (!setupData) return;
    setVerifying(true);
    try {
      await authClient.enableTwoFactor({
        code: verificationCode,
        secret: setupData.secret
      });

      notifications.show({
        title: 'Success',
        message: 'Two-factor authentication enabled successfully',
        color: 'green',
      });
      setTwoFactorModalOpened(false);
      // Reload page or update local state to reflect change
      window.location.reload();
    } catch (error: any) {
      notifications.show({
        title: 'Verification Failed',
        message: error.message || 'Invalid verification code',
        color: 'red',
      });
    } finally {
      setVerifying(false);
    }
  };

  const handleDisable2FA = async () => {
    if (!window.confirm('Are you sure you want to disable two-factor authentication? This will make your account less secure.')) {
      return;
    }

    try {
      await authClient.disableTwoFactor({ userId: '' });
      notifications.show({
        title: 'Success',
        message: 'Two-factor authentication disabled',
        color: 'green',
      });
      window.location.reload();
    } catch (error: any) {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to disable 2FA',
        color: 'red',
      });
    }
  };

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Box>
          <Group gap="xs" mb={4}>
            <ThemeIcon variant="light" color="brand" size="md">
              <IconUser size={18} />
            </ThemeIcon>
            <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>My Profile</Title>
          </Group>
          <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Manage your personal information and security settings.</Text>
        </Box>

        <Grid>
          <Grid.Col span={{ base: 12, md: 4 }}>
            <Paper withBorder p="xl" radius="md" h="100%">
              <Stack align="center" gap="md">
                <Avatar size={120} radius={120} color="brand" fw={800} style={{ fontSize: rem(42) }}>
                  {userInitials}
                </Avatar>
                <Box ta="center">
                  <Title order={3} fw={800} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{user?.name || 'Admin User'}</Title>
                  <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-2))" fw={600} size="sm">{user?.email || 'admin@panmail.dev'}</Text>
                </Box>
                <Badge color="brand">Authorized</Badge>
              </Stack>
            </Paper>
          </Grid.Col>

          <Grid.Col span={{ base: 12, md: 8 }}>
            <Stack gap="xl">
              <Paper withBorder p="xl" radius="md">
                <Group gap="xs" mb="lg">
                  <IconUser size={20} color="var(--mantine-color-brand-6)" />
                  <Title order={4} fw={800} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Basic Information</Title>
                </Group>

                <Grid>
                  <Grid.Col span={6}>
                    <TextInput
                      label="Full Name"
                      value={user?.name || ''}
                      readOnly
                      variant="filled"
                    />
                  </Grid.Col>
                  <Grid.Col span={6}>
                    <TextInput
                      label="Email Address"
                      value={user?.email || ''}
                      readOnly
                      variant="filled"
                      leftSection={<IconMail size={16} />}
                    />
                  </Grid.Col>
                </Grid>

                <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" mt="md" fw={500}>
                  Profile information is managed during the setup process. Contact your system administrator to change these details.
                </Text>
              </Paper>

              <Paper withBorder p="xl" radius="md">
                <Group gap="xs" mb="lg">
                  <IconShieldLock size={20} color="var(--mantine-color-brand-6)" />
                  <Title order={4} fw={800} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Security</Title>
                </Group>

                <Stack gap="md">
                  <Box>
                    <Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Password</Text>
                    <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" mb="md">Change your account password to keep your gateway secure.</Text>
                    <Button variant="light" color="brand" size="xs" radius="md" disabled>
                      Change Password
                    </Button>
                  </Box>

                  <Divider />

                  <Box>
                    <Group justify="space-between" align="center" mb={4}>
                      <Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Two-Factor Authentication</Text>
                      {user?.twoFactorEnabled ? (
                        <Badge color="green">Enabled</Badge>
                      ) : (
                        <Badge color="gray">Disabled</Badge>
                      )}
                    </Group>
                    <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" mb="md">
                      Add an extra layer of security to your account using a TOTP authenticator app.
                    </Text>
                    {user?.twoFactorEnabled ? (
                      <Button variant="light" color="red" size="xs" radius="md" onClick={handleDisable2FA}>
                        Disable 2FA
                      </Button>
                    ) : (
                      <Button variant="light" color="brand" size="xs" radius="md" onClick={handleSetup2FA} loading={setupLoading}>
                        Setup 2FA
                      </Button>
                    )}
                  </Box>
                </Stack>
              </Paper>
            </Stack>
          </Grid.Col>
        </Grid>
      </Stack>

      <Modal
        opened={twoFactorModalOpened}
        onClose={() => setTwoFactorModalOpened(false)}
        title={
          <Group gap="xs">
            <IconQrcode size={20} color="var(--mantine-color-brand-6)" />
            <Text fw={800}>Setup Two-Factor Authentication</Text>
          </Group>
        }
        centered
        radius="md"
        size="md"
      >
        {setupData && (
          <Stack gap="md">
            <Alert icon={<IconAlertCircle size={16} />} title="Important" color="blue" radius="md">
              Scan the QR code below with your authenticator app (like Google Authenticator, Authy, or Bitwarden).
            </Alert>

            <Center py="xl">
              <QRCodeSVG value={setupData.qrCodeUrl} size={200} />
            </Center>

            <Box>
              <Text size="sm" fw={700} mb={4}>Can't scan?</Text>
              <Text size="xs" c="dimmed" mb={8}>Enter this code manually into your app:</Text>
              <Paper withBorder p="xs" style={{ backgroundColor: 'var(--mantine-color-gray-0)' }}>
                <Text ta="center" fw={800} style={{ letterSpacing: rem(2), fontFamily: 'monospace' }}>
                  {setupData.secret}
                </Text>
              </Paper>
            </Box>

            <Divider />

            <Box>
              <Text size="sm" fw={700} mb={8}>Verify Setup</Text>
              <Group gap="sm" align="flex-end">
                <TextInput
                  placeholder="Enter 6-digit code"
                  label="Verification Code"
                  size="sm"
                  style={{ flex: 1 }}
                  value={verificationCode}
                  onChange={(e) => setVerificationCode(e.currentTarget.value)}
                />
                <Button
                  color="brand"
                  size="sm"
                  radius="md"
                  onClick={handleVerifyAndEnable}
                  loading={verifying}
                  leftSection={<IconCheck size={16} />}
                >
                  Verify & Enable
                </Button>
              </Group>
            </Box>
          </Stack>
        )}
      </Modal>
    </Container>
  );
};

const Badge = ({ children, color }: any) => {
  return (
    <Box
      px="md"
      py={4}
      style={{
        backgroundColor: `var(--mantine-color-${color}-light)`,
        color: `var(--mantine-color-${color}-light-color)`,
        borderRadius: rem(32),
        fontSize: rem(12),
        fontWeight: 700,
        textTransform: 'uppercase'
      }}
    >
      {children}
    </Box>
  );
};
