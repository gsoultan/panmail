import React, { useState } from 'react';
import {
  TextInput,
  PasswordInput,
  Button,
  Title,
  Text,
  Container,
  Stack,
  Box,
  Group,
  ThemeIcon,
  rem,
  Grid,
  Divider,
  Alert,
} from '@mantine/core';
import { useForm } from '@mantine/form';
import { useNavigate } from '@tanstack/react-router';
import { authClient } from '../services/client';
import { useAuthStore } from '../store/authStore';
import { notifications } from '@mantine/notifications';
import { motion } from 'framer-motion';
import { IconMail, IconCheck, IconArrowRight, IconQrcode, IconAlertCircle } from '@tabler/icons-react';
import { QRCodeSVG } from 'qrcode.react';

export const SignInPage: React.FC = () => {
  const [loading, setLoading] = useState(false);
  const [showTwoFactor, setShowTwoFactor] = useState(false);
  const [showTwoFactorSetup, setShowTwoFactorSetup] = useState(false);
  const [setupData, setSetupData] = useState<{ secret: string; qrCodeUrl: string } | null>(null);
  const [twoFactorCode, setTwoFactorCode] = useState('');
  const navigate = useNavigate();
  const setAuth = useAuthStore((state) => state.setAuth);

  const form = useForm({
    initialValues: {
      email: '',
      password: '',
    },
    validate: {
      email: (value) => (/^\S+@\S+$/.test(value) ? null : 'Invalid email'),
      password: (value) => (value.length < 6 ? 'Password must be at least 6 characters' : null),
    },
  });

  const handleVerifyTwoFactor = async () => {
    setLoading(true);
    try {
      const response = await authClient.verifyTwoFactor({
        email: form.values.email,
        code: twoFactorCode,
        secret: showTwoFactorSetup ? setupData?.secret : undefined,
      });

      if (response.verified && response.token && response.user) {
        setAuth(
          {
            id: response.user.id,
            email: response.user.email,
            name: response.user.name,
            tenant_id: response.user.tenantId,
            role: response.user.role as any,
            twoFactorEnabled: response.user.twoFactorEnabled,
          },
          response.token
        );

        notifications.show({
          title: showTwoFactorSetup ? '2FA Enabled!' : 'Welcome back!',
          message: showTwoFactorSetup ? 'Security setup complete. Signed in.' : `Signed in as ${response.user.name}`,
          color: 'green',
        });
        navigate({ to: '/' });
      } else {
        notifications.show({
          title: 'Verification Failed',
          message: 'Invalid two-factor code',
          color: 'red',
        });
      }
    } catch (error: any) {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to verify two-factor code',
        color: 'red',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleSubmit = async (values: typeof form.values) => {
    setLoading(true);
    try {
      const response = await authClient.signIn({
        email: values.email,
        password: values.password,
      });

      if (response.twoFactorSetupRequired) {
        setSetupData({ secret: response.twoFactorSecret, qrCodeUrl: response.twoFactorQrCodeUrl });
        setShowTwoFactorSetup(true);
        notifications.show({
          title: '2FA Setup Required',
          message: 'Please set up your two-factor authentication to continue',
          color: 'blue',
        });
        return;
      }

      if (response.twoFactorRequired) {
        setShowTwoFactor(true);
        notifications.show({
          title: 'Two-Factor Authentication',
          message: 'Please enter your 2FA code to continue',
          color: 'blue',
        });
        return;
      }

      if (response.user) {
        setAuth(
          {
            id: response.user.id,
            email: response.user.email,
            name: response.user.name,
            tenant_id: response.user.tenantId,
            role: response.user.role as any,
            twoFactorEnabled: response.user.twoFactorEnabled,
          },
          response.token
        );

        notifications.show({
          title: 'Welcome back!',
          message: `Signed in as ${response.user.name}`,
          color: 'green',
        });
        navigate({ to: '/' });
      }
    } catch (error: any) {
      notifications.show({
        title: 'Sign In Failed',
        message: error.message || 'Invalid email or password',
        color: 'red',
      });
    } finally {
      setLoading(false);
    }
  };

  return (
    <Box
      style={{
        height: '100vh',
        backgroundColor: 'var(--mantine-color-body)',
        display: 'flex',
        overflow: 'hidden',
      }}
    >
      <Grid style={{ width: '100%' }}>
        <Grid.Col 
          span={{ base: 12, md: 5 }} 
          style={{
            background: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-6))',
            height: '100vh',
            display: 'flex',
            alignItems: 'center',
            padding: rem(60)
          }}
        >
          <motion.div
            initial={{ opacity: 0, x: -30 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ duration: 0.8, ease: 'easeOut' }}
          >
            <Stack gap="xl">
              <Group gap="xs">
                <ThemeIcon size={52} radius="md" variant="gradient" gradient={{ from: 'brand.6', to: 'brand.4' }}>
                  <IconMail size={32} />
                </ThemeIcon>
                <Title order={1} style={{ letterSpacing: rem(-1.5), fontWeight: 900, fontSize: rem(42) }}>
                  Panmail
                </Title>
              </Group>

              <Box>
                <Title order={2} fw={800} style={{ fontSize: rem(32), lineHeight: 1.2 }}>
                  The world's best email gateway
                </Title>
                <Text size="lg" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-1))" fw={500} mt="md">
                  Professional infrastructure designed for performance, security, and simplicity.
                </Text>
              </Box>

              <Stack gap="sm">
                {[
                  'Standard protocol support (SMTP, IMAP, POP3)',
                  'Unified gRPC & ConnectRPC API',
                  'High performance & lightweight footprint',
                  'Real-time observability and logs',
                ].map((feature, i) => (
                  <Group key={i} gap="xs">
                    <ThemeIcon size={20} radius="xl" color="brand" variant="light">
                      <IconCheck size={12} />
                    </ThemeIcon>
                    <Text size="sm" fw={600} c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-1))">{feature}</Text>
                  </Group>
                ))}
              </Stack>
            </Stack>
          </motion.div>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 7 }} style={{ height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))' }}>
          <Container size={420} style={{ width: '100%', padding: rem(40) }}>
            <motion.div
              initial={{ opacity: 0, y: 20 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.5, delay: 0.2 }}
            >
              <Box style={{ marginBottom: rem(30) }}>
                <Title order={2} style={{ fontWeight: 800, fontSize: rem(32), marginBottom: rem(8) }}>
                  {showTwoFactorSetup ? 'Security Setup' : 'Welcome back'}
                </Title>
                <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={600} size="md">
                  {showTwoFactorSetup 
                    ? 'Register your two-factor authentication to continue.' 
                    : 'Sign in to your dashboard to manage your gateway.'}
                </Text>
              </Box>

              <Divider mb={30} />

              <form onSubmit={(showTwoFactor || showTwoFactorSetup) ? (e) => { e.preventDefault(); handleVerifyTwoFactor(); } : form.onSubmit(handleSubmit)}>
                <Stack gap="md">
                  {(!showTwoFactor && !showTwoFactorSetup) ? (
                    <>
                      <TextInput
                        label="Email Address"
                        placeholder="you@panmail.dev"
                        required
                        size="sm"
                        {...form.getInputProps('email')}
                      />
                      <PasswordInput
                        label="Password"
                        placeholder="Your secure password"
                        required
                        size="sm"
                        {...form.getInputProps('password')}
                      />
                    </>
                  ) : showTwoFactorSetup ? (
                    <Stack gap="md">
                      <Alert icon={<IconAlertCircle size={16} />} title="Mandatory Security" color="blue" radius="md">
                        Your administrator has enabled two-factor authentication for your account.
                      </Alert>

                      {setupData && (
                        <Box>
                          <Text size="sm" fw={700} mb={8}>1. Scan QR Code</Text>
                          <Box style={{ display: 'flex', justifyContent: 'center', padding: rem(20), backgroundColor: 'white', borderRadius: rem(8) }}>
                            <QRCodeSVG value={setupData.qrCodeUrl} size={160} />
                          </Box>
                          
                          <Text size="sm" fw={700} mt="md" mb={4}>2. Verification Code</Text>
                          <TextInput
                            placeholder="Enter 6-digit code"
                            required
                            size="sm"
                            value={twoFactorCode}
                            onChange={(e) => setTwoFactorCode(e.currentTarget.value)}
                            autoFocus
                          />
                        </Box>
                      )}
                    </Stack>
                  ) : (
                    <TextInput
                      label="Two-Factor Code"
                      placeholder="Enter 6-digit code"
                      required
                      size="sm"
                      value={twoFactorCode}
                      onChange={(e) => setTwoFactorCode(e.currentTarget.value)}
                      autoFocus
                    />
                  )}
                  <Button
                    type="submit"
                    fullWidth
                    mt="xl"
                    size="sm"
                    loading={loading}
                    radius="md"
                    color="brand"
                    rightSection={<IconArrowRight size={16} />}
                  >
                    {(showTwoFactor || showTwoFactorSetup) ? 'Verify & Sign In' : 'Sign In'}
                  </Button>
                  {(showTwoFactor || showTwoFactorSetup) && (
                    <Button variant="subtle" size="xs" onClick={() => { setShowTwoFactor(false); setShowTwoFactorSetup(false); }}>
                      Back to Sign In
                    </Button>
                  )}
                </Stack>
              </form>

              <Text ta="center" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" size="xs" mt={60} fw={600}>
                © 2026 Panmail Email Gateway. Professional Grade Infrastructure.
              </Text>
            </motion.div>
          </Container>
        </Grid.Col>
      </Grid>
    </Box>
  );
};

