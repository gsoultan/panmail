import React from 'react';
import { useForm } from '@mantine/form';
import { TextInput, Select, NumberInput, Checkbox, Button, Stack, Group, Paper, Title, Divider, Text } from '@mantine/core';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';

interface ProviderFormProps {
  initialValues?: any;
  onSubmit: (values: any) => void;
  onTest?: (values: any) => void;
  loading?: boolean;
  testing?: boolean;
}

export const ProviderForm: React.FC<ProviderFormProps> = ({ initialValues, onSubmit, onTest, loading, testing }) => {
  const defaultValues = {
    name: '',
    type: ProviderType.SMTP,
    smtp: { host: '', port: 587, username: '', password: '', skipVerify: false, useSsl: false },
    imap: { host: '', port: 993, username: '', password: '', skipVerify: false, useSsl: true },
    pop3: { host: '', port: 995, username: '', password: '', skipVerify: false, useSsl: true },
  };

  const getInitialValues = () => {
    if (!initialValues) return defaultValues;

    return {
      name: initialValues.name || '',
      type: initialValues.type || ProviderType.SMTP,
      smtp: initialValues.config?.case === 'smtp' ? initialValues.config.value : (initialValues.smtp || defaultValues.smtp),
      imap: initialValues.config?.case === 'imap' ? initialValues.config.value : (initialValues.imap || defaultValues.imap),
      pop3: initialValues.config?.case === 'pop3' ? initialValues.config.value : (initialValues.pop3 || defaultValues.pop3),
    };
  };

  const form = useForm({
    initialValues: getInitialValues(),
    validate: {
      name: (value) => (value.length < 2 ? 'Name must have at least 2 characters' : null),
    }
  });

  const renderConfigFields = () => {
    const commonFields = (prefix: string) => (
      <Stack gap="md">
        <Group grow>
          <TextInput
            label="Host"
            placeholder={`${prefix}.example.com`}
            required
            size="md"
            radius="md"
            {...form.getInputProps(`${prefix}.host`)}
          />
          <NumberInput
            label="Port"
            required
            size="md"
            radius="md"
            {...form.getInputProps(`${prefix}.port`)}
          />
        </Group>
        <Group grow>
          <TextInput
            label="Username"
            placeholder="user@example.com"
            size="md"
            radius="md"
            {...form.getInputProps(`${prefix}.username`)}
            required={prefix !== 'smtp'}
          />
          <TextInput
            label="Password"
            type="password"
            placeholder="••••••••"
            size="md"
            radius="md"
            {...form.getInputProps(`${prefix}.password`)}
            required={prefix !== 'smtp'}
          />
        </Group>
        <Group gap="xl">
          <Checkbox
            label="Use SSL/TLS (Implicit)"
            size="md"
            {...form.getInputProps(`${prefix}.useSsl`, { type: 'checkbox' })}
          />
          <Checkbox
            label="Skip TLS Certificate Verification"
            size="md"
            {...form.getInputProps(`${prefix}.skipVerify`, { type: 'checkbox' })}
          />
        </Group>
      </Stack>
    );

    switch (form.values.type) {
      case ProviderType.SMTP:
        return commonFields('smtp');
      case ProviderType.IMAP:
        return commonFields('imap');
      case ProviderType.POP3:
        return commonFields('pop3');
      default:
        return null;
    }
  };

  return (
    <Paper withBorder p="xl" radius="md">
      <form onSubmit={form.onSubmit(onSubmit)}>
        <Stack gap="xl">
          <Stack gap={4}>
            <Title order={3} fw={800}>{initialValues ? 'Edit' : 'Connect'} Email Provider</Title>
            <Text size="sm" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">Configure your SMTP, IMAP, or POP3 server to start sending and receiving emails.</Text>
          </Stack>

          <Divider />

          <Stack gap="md">
            <Group grow align="flex-start">
              <TextInput
                label="Provider Name"
                placeholder="e.g. Primary Gmail, Office 365"
                required
                size="md"
                radius="md"
                {...form.getInputProps('name')}
              />
              <Select
                label="Connection Protocol"
                data={[
                  { value: ProviderType.SMTP.toString(), label: 'SMTP (Outgoing)' },
                  { value: ProviderType.IMAP.toString(), label: 'IMAP (Incoming)' },
                  { value: ProviderType.POP3.toString(), label: 'POP3 (Incoming)' },
                ]}
                {...form.getInputProps('type')}
                onChange={(val) => form.setFieldValue('type', parseInt(val || '0'))}
                value={form.values.type.toString()}
                required
                size="md"
                radius="md"
              />
            </Group>
          </Stack>

          <Paper bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))" p="xl" radius="md" withBorder>
            <Stack gap="md">
              <Text fw={700} size="sm" tt="uppercase" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">Server Configuration</Text>
              {renderConfigFields()}
            </Stack>
          </Paper>

          <Group justify="space-between" pt="md">
            <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" maw={400}>
              Panmail uses secure connections by default. Ensure your provider supports TLS/SSL on the specified port.
            </Text>
            <Group gap="sm">
              {onTest && (
                <Button 
                  variant="light" 
                  color="indigo" 
                  onClick={() => onTest(form.values)} 
                  loading={testing}
                  size="md"
                  radius="md"
                >
                  Test Connection
                </Button>
              )}
              <Button type="submit" loading={loading} size="md" radius="md" color="brand">
                {initialValues ? 'Update Connection' : 'Establish Connection'}
              </Button>
            </Group>
          </Group>
        </Stack>
      </form>
    </Paper>
  );
};
