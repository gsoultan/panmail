import React from 'react';
import { TextInput, Select, NumberInput, Checkbox, Button, Stack, Group, Paper, Title, Divider, Text, CopyButton, Tooltip, ActionIcon } from '@mantine/core';
import { IconCopy, IconCheck } from '@tabler/icons-react';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import { useAdaptedForm, type FieldErrors } from '../../../lib/form/useAdaptedForm';
import { seedSmtpValues, toProviderRequest } from './providerFormValues';
import { DkimSection } from './DkimSection';
import { ApiProviderFields } from './ApiProviderFields';
import { OAuthSection } from './OAuthSection';
import { DomainHealthPanel } from './DomainHealthPanel';

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
    smtp: {
      host: '', port: 587, username: '', password: '', skipVerify: false, useSsl: false,
      // dkimEnabled is form-only: the server infers signing from whether all
      // three DKIM values are present, so it is never sent.
      dkimEnabled: false,
      dkim: { domain: '', selector: '', privateKey: '' },
      // authMode is form-only. The server infers OAuth from whether the client
      // id, refresh token and endpoint are all present.
      authMode: 'password',
      oauth2: { mechanism: 'XOAUTH2', clientId: '', clientSecret: '', refreshToken: '', tokenEndpoint: '', scope: '' },
    },
    imap: { host: '', port: 993, username: '', password: '', skipVerify: false, useSsl: true },
    pop3: { host: '', port: 995, username: '', password: '', skipVerify: false, useSsl: true },
    sendgrid: { apiKey: '', baseUrl: '' },
    ses: { region: 'us-east-1', accessKey: '', secretKey: '', endpoint: '' },
    postmark: { serverToken: '', messageStream: '', baseUrl: '' },
    mailgun: { domain: '', apiKey: '', baseUrl: '' },
  };

  const getInitialValues = () => {
    if (!initialValues) return defaultValues;

    // The SMTP branch goes through seedSmtpValues so the two form-only switches
    // are materialised from the config that came back. Without that they stay
    // undefined, submit reads them as off, and saving an untouched provider
    // clears its DKIM key or its OAuth credentials.
    const smtpConfig = initialValues.config?.case === 'smtp'
      ? initialValues.config.value
      : initialValues.smtp;

    return {
      id: initialValues.id || '',
      name: initialValues.name || '',
      type: initialValues.type || ProviderType.SMTP,
      smtp: seedSmtpValues(smtpConfig, defaultValues.smtp),
      imap: initialValues.config?.case === 'imap' ? initialValues.config.value : (initialValues.imap || defaultValues.imap),
      pop3: initialValues.config?.case === 'pop3' ? initialValues.config.value : (initialValues.pop3 || defaultValues.pop3),
      sendgrid: initialValues.config?.case === 'sendgrid' ? initialValues.config.value : (initialValues.sendgrid || defaultValues.sendgrid),
      ses: initialValues.config?.case === 'ses' ? initialValues.config.value : (initialValues.ses || defaultValues.ses),
      postmark: initialValues.config?.case === 'postmark' ? initialValues.config.value : (initialValues.postmark || defaultValues.postmark),
      mailgun: initialValues.config?.case === 'mailgun' ? initialValues.config.value : (initialValues.mailgun || defaultValues.mailgun),
    };
  };

  const validate = React.useCallback((values: any): FieldErrors => ({
    name: values.name?.length < 2 ? 'Name must have at least 2 characters' : undefined,
  }), []);

  const form = useAdaptedForm<any>({
    initialValues: getInitialValues(),
    validate,
    // toProviderRequest strips the form-only switches and clears the values
    // behind whichever one is off; see providerFormValues.ts.
    onSubmit: (values) => onSubmit(toProviderRequest(values)),
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
        return (
          <>
            {commonFields('smtp')}
            <OAuthSection form={form} editing={Boolean(initialValues?.id)} />
            <DkimSection form={form} editing={Boolean(initialValues?.id)} />
            {/* Below DKIM because it is what tells you whether the key above
                was ever published. */}
            <DomainHealthPanel
              providerId={initialValues?.id}
              domain={form.values.smtp?.dkim?.domain}
              selector={form.values.smtp?.dkim?.selector}
            />
          </>
        );
      case ProviderType.IMAP:
        return commonFields('imap');
      case ProviderType.POP3:
        return commonFields('pop3');
      case ProviderType.SENDGRID:
      case ProviderType.SES:
      case ProviderType.POSTMARK:
      case ProviderType.MAILGUN:
        return (
          <ApiProviderFields
            form={form}
            type={form.values.type}
            editing={Boolean(initialValues?.id)}
          />
        );
      default:
        return null;
    }
  };

  return (
    <Paper withBorder p="xl" radius="md">
      <form onSubmit={form.onSubmit()}>
        <Stack gap="xl">
          <Stack gap={4}>
            <Title order={3} fw={800}>{initialValues ? 'Edit' : 'Connect'} Email Provider</Title>
            <Text size="sm" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">Configure your SMTP, IMAP, or POP3 server to start sending and receiving emails.</Text>
          </Stack>

          {initialValues && (
            <TextInput
              label="Provider ID"
              value={initialValues.id}
              readOnly
              variant="filled"
              size="md"
              radius="md"
              rightSection={
                <CopyButton value={initialValues.id}>
                  {({ copied, copy }) => (
                    <Tooltip label={copied ? 'Copied' : 'Copy ID'} withArrow position="right">
                      <ActionIcon color={copied ? 'teal' : 'gray'} variant="subtle" onClick={copy}>
                        {copied ? <IconCheck size={16} stroke={2} /> : <IconCopy size={16} stroke={2} />}
                      </ActionIcon>
                    </Tooltip>
                  )}
                </CopyButton>
              }
            />
          )}

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
                  { group: 'Outgoing (SMTP)', items: [
                    { value: ProviderType.SMTP.toString(), label: 'SMTP' },
                  ]},
                  { group: 'Outgoing (provider API)', items: [
                    { value: ProviderType.SENDGRID.toString(), label: 'SendGrid' },
                    { value: ProviderType.SES.toString(), label: 'Amazon SES' },
                    { value: ProviderType.POSTMARK.toString(), label: 'Postmark' },
                    { value: ProviderType.MAILGUN.toString(), label: 'Mailgun' },
                  ]},
                  { group: 'Incoming', items: [
                    { value: ProviderType.IMAP.toString(), label: 'IMAP' },
                    { value: ProviderType.POP3.toString(), label: 'POP3' },
                  ]},
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
