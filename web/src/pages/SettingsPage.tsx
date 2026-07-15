import React from 'react';
import { Container, Title, Text, Stack, Paper, Group, ThemeIcon, rem, Box, TextInput, NumberInput, Button, LoadingOverlay, Divider, Alert, TagsInput } from '@mantine/core';
import { IconSettings, IconDeviceFloppy, IconInfoCircle, IconDatabase, IconWorld, IconRefresh } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useForm } from '@mantine/form';
import { settingsService } from '../services/settings';
import { notifications } from '@mantine/notifications';

export const SettingsPage: React.FC = () => {
  const queryClient = useQueryClient();
  const { data: settings, isLoading } = useQuery({
    queryKey: ['systemSettings'],
    queryFn: settingsService.getSettings,
  });

  const form = useForm({
    initialValues: {
      baseUrl: '',
      logRetentionDays: 14,
      retryPattern: [] as string[],
    },
    validate: {
      logRetentionDays: (value) => (value < 1 ? 'Retention must be at least 1 day' : null),
    },
  });

  React.useEffect(() => {
    if (settings) {
      form.setValues({
        baseUrl: settings.baseUrl || '',
        logRetentionDays: settings.logRetentionDays || 14,
        retryPattern: settings.retryPattern || [],
      });
    }
  }, [settings]);

  const mutation = useMutation({
    mutationFn: (values: typeof form.values) => settingsService.updateSettings(values as any),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['systemSettings'] });
      notifications.show({
        title: 'Settings updated',
        message: 'Application settings have been saved successfully.',
        color: 'green',
      });
    },
    onError: (error: any) => {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to update settings',
        color: 'red',
      });
    },
  });

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Box>
          <Group gap="xs" mb={4}>
            <ThemeIcon variant="light" color="brand" size="md">
              <IconSettings size={18} />
            </ThemeIcon>
            <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Settings</Title>
          </Group>
          <Text c="dimmed" fw={500}>Configure global application parameters and data retention policies.</Text>
        </Box>

        <Paper withBorder p="xl" radius="md" pos="relative">
          <LoadingOverlay visible={isLoading || mutation.isPending} />
          <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
            <Stack gap="lg">
              <Box>
                <Group gap="xs" mb="xs">
                  <IconWorld size={18} color="var(--mantine-color-brand-6)" />
                  <Text fw={700} size="sm" tt="uppercase">General Configuration</Text>
                </Group>
                <TextInput
                  label="Base URL"
                  placeholder="https://panmail.example.com"
                  description="The public URL where the gateway is accessible."
                  {...form.getInputProps('baseUrl')}
                />
              </Box>

              <Divider />

              <Box>
                <Group gap="xs" mb="xs">
                  <IconRefresh size={18} color="var(--mantine-color-brand-6)" />
                  <Text fw={700} size="sm" tt="uppercase">Retry Configuration</Text>
                </Group>
                <TagsInput
                  label="Retry Pattern"
                  placeholder="e.g. 5m, 15m, 1h, 6h"
                  description="Sequence of delays for retrying soft bounces. Supported units: m (minutes), h (hours), d (days). Each entry represents the delay for the N-th retry."
                  {...form.getInputProps('retryPattern')}
                />
              </Box>

              <Divider />

              <Box>
                <Group gap="xs" mb="xs">
                  <IconDatabase size={18} color="var(--mantine-color-brand-6)" />
                  <Text fw={700} size="sm" tt="uppercase">Data Retention</Text>
                </Group>
                <NumberInput
                  label="Log Retention Period (Days)"
                  description="Delivery event logs older than this will be archived and removed from the active database."
                  min={1}
                  max={365}
                  {...form.getInputProps('logRetentionDays')}
                />
                <Alert icon={<IconInfoCircle size={16} />} title="Note" mt="md" color="blue" variant="light">
                  Archived logs are saved to the filesystem as JSONL files and can still be accessed via the Archives page.
                </Alert>
              </Box>

              <Group justify="flex-end" mt="xl">
                <Button 
                  type="submit" 
                  leftSection={<IconDeviceFloppy size={18} />}
                  loading={mutation.isPending}
                >
                  Save Settings
                </Button>
              </Group>
            </Stack>
          </form>
        </Paper>
      </Stack>
    </Container>
  );
};
