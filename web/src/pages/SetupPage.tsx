import React, { useState } from 'react';
import {
  Container,
  Stepper,
  Button,
  Group,
  TextInput,
  PasswordInput,
  Title,
  Stack,
  Select,
  NumberInput,
  Text,
  Box,
  ThemeIcon,
  rem,
  Alert,
  Divider,
  Grid,
} from '@mantine/core';
import { useForm } from '@mantine/form';
import { useNavigate } from '@tanstack/react-router';
import { setupClient } from '../services/client';
import { notifications } from '@mantine/notifications';
import { motion, AnimatePresence } from 'framer-motion';
import {
  IconDatabase,
  IconUser,
  IconChecks,
  IconMail,
  IconAlertCircle,
  IconCheck,
  IconArrowRight,
  IconChevronLeft,
} from '@tabler/icons-react';



export const SetupPage: React.FC = () => {
  const [active, setActive] = useState(0);
  const [loading, setLoading] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ success: boolean; message: string } | null>(null);
  const navigate = useNavigate();

  const dbForm = useForm({
    initialValues: {
      type: 'sqlite',
      host: 'localhost',
      port: 5432,
      user: '',
      password: '',
      dbname: 'panmail',
      filePath: 'panmail.db',
    },
  });

  const adminForm = useForm({
    initialValues: {
      email: '',
      password: '',
      name: '',
    },
    validate: {
      email: (value) => (/^\S+@\S+$/.test(value) ? null : 'Invalid email'),
      password: (value) => (value.length < 6 ? 'Password must be at least 6 characters' : null),
      name: (value) => (value.length < 2 ? 'Name is too short' : null),
    },
  });

  const configForm = useForm({
    initialValues: {
      baseUrl: window.location.origin,
    },
    validate: {
      baseUrl: (value) => (value.length === 0 ? 'Base URL is required' : null),
    },
  });

  const nextStep = () => {
    if (active === 1) {
      const validation = adminForm.validate();
      if (validation.hasErrors) return;
    }
    setActive((current) => (current < 2 ? current + 1 : current));
  };
  const prevStep = () => setActive((current) => (current > 0 ? current - 1 : current));

  const handleTestConnection = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const res = await setupClient.testDatabaseConnection({
        dbConfig: {
          type: dbForm.values.type,
          host: dbForm.values.host,
          port: dbForm.values.port,
          user: dbForm.values.user,
          password: dbForm.values.password,
          dbname: dbForm.values.dbname,
          filePath: dbForm.values.filePath,
        },
      });
      setTestResult({ success: res.success, message: res.message });
      if (res.success) {
        notifications.show({
          title: 'Success',
          message: 'Database connection verified!',
          color: 'green',
        });
      } else {
        notifications.show({
          title: 'Connection Failed',
          message: res.message,
          color: 'red',
        });
      }
    } catch (error: any) {
      setTestResult({ success: false, message: error.message || 'Failed to test connection' });
    } finally {
      setTesting(false);
    }
  };

  const handleComplete = async () => {
    setLoading(true);
    try {
      await setupClient.setup({
        dbConfig: {
          type: dbForm.values.type,
          host: dbForm.values.host,
          port: dbForm.values.port,
          user: dbForm.values.user,
          password: dbForm.values.password,
          dbname: dbForm.values.dbname,
          filePath: dbForm.values.filePath,
        },
        adminConfig: {
          email: adminForm.values.email,
          password: adminForm.values.password,
          name: adminForm.values.name,
        },
        baseUrl: configForm.values.baseUrl,
      });

      notifications.show({
        title: 'Welcome to Panmail!',
        message: 'Your gateway is now ready. Please sign in.',
        color: 'green',
      });
      navigate({ to: '/signin' });
    } catch (error: any) {
      notifications.show({
        title: 'Setup Failed',
        message: error.message || 'An error occurred during setup',
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
          <Container size={540} style={{ width: '100%', padding: rem(40) }}>
            <Box style={{ marginBottom: rem(30) }}>
              <Title order={2} style={{ fontWeight: 800, fontSize: rem(32), marginBottom: rem(8) }}>
                Let's set up your gateway
              </Title>
              <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={600} size="md">
                Configure your environment and admin account in minutes.
              </Text>
            </Box>

            <Box mb={30}>
              <Stepper
                active={active}
                onStepClick={setActive}
                allowNextStepsSelect={false}
                size="sm"
                color="brand"
                styles={{
                  stepIcon: { border: 0, backgroundColor: 'transparent' },
                  step: { padding: 0 },
                  stepLabel: { fontSize: rem(13), fontWeight: 700, color: 'light-dark(var(--mantine-color-black), var(--mantine-color-white))' },
                  stepDescription: { fontSize: rem(11), fontWeight: 500, color: 'light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-1))' },
                }}
              >
                <Stepper.Step
                  label="Database"
                  description="Storage"
                  icon={<IconDatabase size={18} />}
                  completedIcon={<IconCheck size={18} />}
                />
                <Stepper.Step
                  label="Admin"
                  description="Identity"
                  icon={<IconUser size={18} />}
                  completedIcon={<IconCheck size={18} />}
                />
                <Stepper.Step
                  label="Ready"
                  description="Launch"
                  icon={<IconChecks size={18} />}
                  completedIcon={<IconCheck size={18} />}
                />
              </Stepper>
            </Box>

            <Divider mb={30} />

            <Box style={{ position: 'relative', minHeight: rem(280) }}>
              <AnimatePresence mode="wait">
                <motion.div
                  key={active}
                  initial={{ opacity: 0, x: 20 }}
                  animate={{ opacity: 1, x: 0 }}
                  exit={{ opacity: 0, x: -20 }}
                  transition={{ duration: 0.3, ease: 'easeInOut' }}
                >
                  {active === 0 && (
                    <Stack gap="md">
                      <Select
                        label="Database Engine"
                        description="Where should we store your data?"
                        size="sm"
                        data={[
                          { value: 'sqlite', label: 'SQLite (Single file, no setup)' },
                          { value: 'postgres', label: 'PostgreSQL' },
                          { value: 'mysql', label: 'MySQL' },
                          { value: 'mariadb', label: 'MariaDB' },
                        ]}
                        {...dbForm.getInputProps('type')}
                      />

                      {dbForm.values.type === 'sqlite' ? (
                        <TextInput
                          label="Database File Path"
                          placeholder="panmail.db"
                          size="sm"
                          {...dbForm.getInputProps('filePath')}
                        />
                      ) : (
                        <>
                          <Group grow gap="md">
                            <TextInput
                              label="Host"
                              placeholder="localhost"
                              size="sm"
                              {...dbForm.getInputProps('host')}
                            />
                            <NumberInput
                              label="Port"
                              placeholder={dbForm.values.type === 'postgres' ? '5432' : '3306'}
                              size="sm"
                              {...dbForm.getInputProps('port')}
                            />
                          </Group>
                          <Group grow gap="md">
                            <TextInput
                              label="Username"
                              placeholder="dbuser"
                              size="sm"
                              {...dbForm.getInputProps('user')}
                            />
                            <PasswordInput
                              label="Password"
                              placeholder="Secure password"
                              size="sm"
                              {...dbForm.getInputProps('password')}
                            />
                          </Group>
                          <TextInput
                            label="Database Name"
                            placeholder="panmail"
                            size="sm"
                            {...dbForm.getInputProps('dbname')}
                          />
                        </>
                      )}

                      {testResult && (
                        <motion.div initial={{ opacity: 0, scale: 0.95 }} animate={{ opacity: 1, scale: 1 }}>
                          <Alert
                            icon={testResult.success ? <IconCheck size={14} /> : <IconAlertCircle size={14} />}
                            title={testResult.success ? 'Connected' : 'Connection Failed'}
                            color={testResult.success ? 'green' : 'red'}
                            variant="light"
                            radius="md"
                            styles={{ title: { fontSize: rem(13) }, label: { fontSize: rem(12) } }}
                          >
                            <Text size="xs">{testResult.message}</Text>
                          </Alert>
                        </motion.div>
                      )}

                      <Button
                        variant="light"
                        color="indigo"
                        size="sm"
                        radius="md"
                        onClick={handleTestConnection}
                        loading={testing}
                      >
                        Test Connection
                      </Button>
                    </Stack>
                  )}

                  {active === 1 && (
                    <Stack gap="md">
                      <TextInput
                        label="Full Name"
                        placeholder="e.g. John Doe"
                        size="sm"
                        {...adminForm.getInputProps('name')}
                      />
                      <TextInput
                        label="Email Address"
                        placeholder="admin@panmail.dev"
                        size="sm"
                        {...adminForm.getInputProps('email')}
                      />
                      <PasswordInput
                        label="Password"
                        placeholder="At least 6 characters"
                        size="sm"
                        {...adminForm.getInputProps('password')}
                      />
                    </Stack>
                  )}

                  {active === 2 && (
                    <Stack gap="md" py={10}>
                      <Group justify="center">
                        <motion.div
                          initial={{ scale: 0 }}
                          animate={{ scale: 1 }}
                          transition={{ type: 'spring', damping: 12, stiffness: 200 }}
                        >
                          <ThemeIcon size={60} radius={60} color="green" variant="light">
                            <IconChecks size={36} />
                          </ThemeIcon>
                        </motion.div>
                      </Group>
                      <Box ta="center">
                        <Title order={3} style={{ fontWeight: 800, fontSize: rem(22) }}>Almost there!</Title>
                        <Text c="gray.7" fw={500} size="sm" mt="xs">
                          Finalize your gateway configuration.
                        </Text>
                      </Box>
                      
                      <TextInput
                        label="Gateway Base URL"
                        description="Used for tracking pixels and links"
                        placeholder="http://localhost:8080"
                        size="sm"
                        {...configForm.getInputProps('baseUrl')}
                      />
                    </Stack>
                  )}
                </motion.div>
              </AnimatePresence>
            </Box>

            <Group justify="space-between" mt={40}>
              {active !== 0 ? (
                <Button
                  variant="subtle"
                  onClick={prevStep}
                  disabled={loading}
                  leftSection={<IconChevronLeft size={16} />}
                  size="sm"
                  radius="md"
                >
                  Back
                </Button>
              ) : <Box />}

              {active < 2 ? (
                <Button
                  onClick={nextStep}
                  rightSection={<IconArrowRight size={16} />}
                  size="sm"
                  radius="md"
                  px={25}
                >
                  Continue
                </Button>
              ) : (
                <Button
                  color="blue"
                  onClick={handleComplete}
                  loading={loading}
                  size="sm"
                  radius="md"
                  px={35}
                  variant="filled"
                >
                  Complete Setup
                </Button>
              )}
            </Group>

            <Text ta="center" c="gray.8" size="xs" mt={60} fw={600}>
              © 2026 Panmail Email Gateway. Professional Grade Infrastructure.
            </Text>
          </Container>
        </Grid.Col>
      </Grid>
    </Box>
  );
};
