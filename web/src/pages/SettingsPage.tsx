import React from 'react';
import {
  Alert,
  Box,
  Button,
  Container,
  Divider,
  Group,
  List,
  LoadingOverlay,
  Modal,
  Paper,
  Select,
  Stack,
  TagsInput,
  Text,
  TextInput,
  ThemeIcon,
  Title,
  rem,
} from '@mantine/core';
import {
  IconAlertTriangle,
  IconDatabase,
  IconDeviceFloppy,
  IconEyeOff,
  IconInfoCircle,
  IconRefresh,
  IconSettings,
  IconWorld,
} from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useAdaptedForm } from '../lib/form/useAdaptedForm';
import { settingsService } from '../services/settings';
import { notifications } from '@mantine/notifications';
import { RetentionSettings } from '../features/settings/components/RetentionSettings';
import {
  destructiveChanges,
  describeRetention,
  retentionValues,
  type RetentionChange,
  type RetentionKey,
} from '../features/settings/components/retentionFields';

export const SettingsPage: React.FC = () => {
  const queryClient = useQueryClient();
  const {
    data: settings,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ['systemSettings'],
    queryFn: settingsService.getSettings,
  });

  // Saving is blocked until the current settings have actually loaded.
  //
  // The form starts at its initial values, which are an empty base URL and
  // zero for every retention. If the load fails, the overlay lifts and those
  // placeholders look like real settings — so one click of Save would blank
  // the base URL that one-click unsubscribe depends on and record all seven
  // retentions as a deliberate "keep forever", which the config migration
  // then has no way to tell from a real choice.
  const loaded = settings !== undefined;

  const form = useAdaptedForm({
    initialValues: {
      baseUrl: '',
      retryPattern: [] as string[],
      // 2 is CONTENT_REDACTION_PASSWORDS. The form never holds UNSPECIFIED:
      // the API answers with the level actually in force, and posting 0 back
      // would mean "leave it alone", which a visible control should not do.
      contentRedaction: 2,
      ...retentionValues(undefined),
    },
  });

  React.useEffect(() => {
    if (settings) {
      form.setValues({
        baseUrl: settings.baseUrl || '',
        retryPattern: settings.retryPattern || [],
        contentRedaction: settings.contentRedaction || 2,
        ...retentionValues(settings),
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

  // Saving triggers a retention pass immediately, so a shortened policy is not
  // a preference that takes effect quietly overnight — it deletes on save.
  // Anything that destroys the only copy panmail holds gets confirmed first.
  const [pending, setPending] = React.useState<{
    values: typeof form.values;
    changes: RetentionChange[];
  } | null>(null);

  const submit = (values: typeof form.values) => {
    const changes = destructiveChanges(retentionValues(settings), values);
    if (changes.length > 0) {
      setPending({ values, changes });
      return;
    }
    mutation.mutate(values);
  };

  const confirm = () => {
    if (!pending) return;
    mutation.mutate(pending.values);
    setPending(null);
  };

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Box>
          <Group gap="xs" mb={4}>
            <ThemeIcon variant="light" color="brand" size="md">
              <IconSettings size={18} />
            </ThemeIcon>
            <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>
              Settings
            </Title>
          </Group>
          <Text c="dimmed" fw={500}>
            Configure global application parameters and data retention policies.
          </Text>
        </Box>

        {isError ? (
          <Alert icon={<IconAlertTriangle size={16} />} color="red" variant="light" title="Settings could not be loaded">
            Saving is disabled until they load, because the form would otherwise post its
            placeholders over your real configuration. Reload the page to try again.
          </Alert>
        ) : null}

        <Paper withBorder p="xl" radius="md" pos="relative">
          <LoadingOverlay visible={isLoading || mutation.isPending} />
          <form onSubmit={form.onSubmit(submit)}>
            <Stack gap="lg">
              <Box>
                <Group gap="xs" mb="xs">
                  <IconWorld size={18} color="var(--mantine-color-brand-6)" />
                  <Text fw={700} size="sm" tt="uppercase">
                    General Configuration
                  </Text>
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
                  <Text fw={700} size="sm" tt="uppercase">
                    Retry Configuration
                  </Text>
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
                  <IconEyeOff size={18} color="var(--mantine-color-brand-6)" />
                  <Text fw={700} size="sm" tt="uppercase">
                    Message Content
                  </Text>
                </Group>
                <Text size="sm" c="dimmed" mb="md">
                  Delivery details show the body of a sent message, and transactional mail
                  carries what it carries. Masking happens in the gateway, so a redacted value
                  never reaches the browser at all. <strong>Stored mail is never changed</strong>
                  {' '}&mdash; only what the API returns.
                </Text>
                <Select
                  label="Redact secrets in message content"
                  allowDeselect={false}
                  data={[
                    { value: '1', label: 'Off — show the body as sent' },
                    { value: '2', label: 'Passwords (default)' },
                    { value: '3', label: 'Passwords and one-time codes' },
                    { value: '4', label: 'Passwords, codes, tokens and API keys' },
                  ]}
                  description="Only the value after a label is masked, so a message that merely mentions a password is left intact."
                  value={String(form.values.contentRedaction ?? 2)}
                  onChange={(v) => form.setFieldValue('contentRedaction', Number(v ?? 2))}
                  disabled={isLoading || mutation.isPending}
                />
              </Box>

              <Divider />

              <Box>
                <Group gap="xs" mb="xs">
                  <IconDatabase size={18} color="var(--mantine-color-brand-6)" />
                  <Text fw={700} size="sm" tt="uppercase">
                    Data Retention
                  </Text>
                </Group>
                <Text size="sm" c="dimmed" mb="lg">
                  How long each kind of data is kept, in days. <strong>Zero keeps it forever</strong>
                  , which is the default for everything panmail holds the only copy of. Changes
                  apply as soon as they are saved.
                </Text>

                <RetentionSettings
                  values={retentionValues(form.values as Partial<Record<RetentionKey, number>>)}
                  onChange={(key, value) => form.setFieldValue(key, value)}
                  disabled={isLoading || mutation.isPending}
                />

                <Alert
                  icon={<IconInfoCircle size={16} />}
                  title="Where expired data goes"
                  mt="lg"
                  color="blue"
                  variant="light"
                >
                  Delivery events are written to JSONL archives before they leave the database and
                  stay available on the Archives page. Everything else is deleted outright.
                </Alert>
              </Box>

              <Group justify="flex-end" mt="xl">
                <Button
                  type="submit"
                  leftSection={<IconDeviceFloppy size={18} />}
                  loading={mutation.isPending}
                  disabled={!loaded}
                >
                  Save Settings
                </Button>
              </Group>
            </Stack>
          </form>
        </Paper>
      </Stack>

      <Modal
        opened={pending !== null}
        onClose={() => setPending(null)}
        title={
          <Group gap="xs">
            <ThemeIcon variant="light" color="orange" size="md">
              <IconAlertTriangle size={18} />
            </ThemeIcon>
            <Text fw={700}>This deletes data now</Text>
          </Group>
        }
        centered
      >
        <Stack gap="md">
          <Text size="sm">
            Saving starts a retention pass immediately. These changes remove the only copy panmail
            holds:
          </Text>

          <List size="sm" spacing="xs">
            {pending?.changes.map(({ field, from, to }) => (
              <List.Item key={field.key}>
                <Text size="sm" fw={600}>
                  {field.label}
                </Text>
                <Text size="xs" c="dimmed">
                  {describeRetention(from, field)} → {describeRetention(to, field)}
                </Text>
              </List.Item>
            ))}
          </List>

          <Group justify="flex-end">
            <Button variant="default" onClick={() => setPending(null)}>
              Cancel
            </Button>
            <Button color="orange" onClick={confirm}>
              Save and delete
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Container>
  );
};
