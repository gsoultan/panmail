import React from 'react';
import { Stack, Group, TextInput, PasswordInput, Select, Alert, Text, Anchor } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';

interface ApiProviderFieldsProps {
  form: any;
  type: ProviderType;
  /** True when editing a saved provider, so the secret is stored but redacted. */
  editing: boolean;
}

/**
 * Credential forms for the API-backed providers.
 *
 * Two things shape these. Secrets are write-only — reads redact them — so when
 * editing, the field has to say "leave blank to keep" rather than looking like
 * an empty value about to be saved. And each vendor has one setting that is easy
 * to get wrong and fails confusingly: Mailgun's EU region is a different host,
 * Postmark rejects bulk mail on a transactional stream, SES is region-scoped.
 * Those get a note where they are entered, not in documentation elsewhere.
 */

const secretDescription = (editing: boolean, what: string) =>
  editing
    ? `A ${what} is already stored. Leave blank to keep it, or enter a new one to replace it.`
    : `Stored encrypted and never shown again.`;

const secretPlaceholder = (editing: boolean) => (editing ? '•••••••• stored ••••••••' : '');

// Mailgun and SES are region-scoped, and picking the wrong one fails
// authentication rather than falling back to the right region.
const MAILGUN_REGIONS = [
  { value: '', label: 'US (api.mailgun.net)' },
  { value: 'https://api.eu.mailgun.net/v3', label: 'EU (api.eu.mailgun.net)' },
];

const SES_REGIONS = [
  'us-east-1', 'us-east-2', 'us-west-1', 'us-west-2',
  'eu-west-1', 'eu-west-2', 'eu-central-1', 'eu-north-1',
  'ap-south-1', 'ap-southeast-1', 'ap-southeast-2', 'ap-northeast-1',
  'ca-central-1', 'sa-east-1',
];

export const ApiProviderFields: React.FC<ApiProviderFieldsProps> = ({ form, type, editing }) => {
  switch (type) {
    case ProviderType.SENDGRID:
      return (
        <Stack gap="md">
          <PasswordInput
            label="API key"
            description={secretDescription(editing, 'API key')}
            placeholder={secretPlaceholder(editing) || 'SG.xxxxxxxx'}
            size="md"
            radius="md"
            required={!editing}
            {...form.getInputProps('sendgrid.apiKey')}
          />
          <TextInput
            label="API base URL"
            description="Leave empty for api.sendgrid.com. Set this only for a regional endpoint."
            placeholder="https://api.sendgrid.com"
            size="md"
            radius="md"
            {...form.getInputProps('sendgrid.baseUrl')}
          />
          <Alert color="blue" variant="light" icon={<IconInfoCircle size={16} />}>
            <Text size="xs">
              The key needs the <b>Mail Send</b> permission. A key restricted to other scopes
              authenticates successfully and then fails on every send.
            </Text>
          </Alert>
        </Stack>
      );

    case ProviderType.SES:
      return (
        <Stack gap="md">
          <Group grow align="flex-start">
            <Select
              label="Region"
              description="SES is region-scoped; a verified identity exists in one region only"
              data={SES_REGIONS}
              searchable
              size="md"
              radius="md"
              required
              {...form.getInputProps('ses.region')}
            />
            <TextInput
              label="Access key ID"
              placeholder="AKIA..."
              size="md"
              radius="md"
              required
              {...form.getInputProps('ses.accessKey')}
            />
          </Group>
          <PasswordInput
            label="Secret access key"
            description={secretDescription(editing, 'secret key')}
            placeholder={secretPlaceholder(editing)}
            size="md"
            radius="md"
            required={!editing}
            {...form.getInputProps('ses.secretKey')}
          />
          <TextInput
            label="Endpoint override"
            description="For a VPC endpoint or a local test double. Leave empty for the regional default."
            placeholder="Optional"
            size="md"
            radius="md"
            {...form.getInputProps('ses.endpoint')}
          />
          <Alert color="blue" variant="light" icon={<IconInfoCircle size={16} />}>
            <Text size="xs">
              A new SES account is in the sandbox and can only send to verified addresses.
              Request production access before going live, or delivery silently fails for
              everyone else.
            </Text>
          </Alert>
        </Stack>
      );

    case ProviderType.POSTMARK:
      return (
        <Stack gap="md">
          <PasswordInput
            label="Server token"
            description={secretDescription(editing, 'server token')}
            placeholder={secretPlaceholder(editing)}
            size="md"
            radius="md"
            required={!editing}
            {...form.getInputProps('postmark.serverToken')}
          />
          <TextInput
            label="Message stream"
            description="Postmark rejects bulk mail sent on a transactional stream. Use 'broadcast' for campaigns; leave empty for the server default."
            placeholder="outbound"
            size="md"
            radius="md"
            {...form.getInputProps('postmark.messageStream')}
          />
          <TextInput
            label="API base URL"
            description="Leave empty for api.postmarkapp.com."
            placeholder="https://api.postmarkapp.com"
            size="md"
            radius="md"
            {...form.getInputProps('postmark.baseUrl')}
          />
        </Stack>
      );

    case ProviderType.MAILGUN:
      return (
        <Stack gap="md">
          <Group grow align="flex-start">
            <TextInput
              label="Sending domain"
              description="The domain verified in Mailgun, not your From domain if they differ"
              placeholder="mg.example.com"
              size="md"
              radius="md"
              required
              {...form.getInputProps('mailgun.domain')}
            />
            <Select
              label="Region"
              description="An EU domain will not authenticate against the US host"
              data={MAILGUN_REGIONS}
              size="md"
              radius="md"
              {...form.getInputProps('mailgun.baseUrl')}
            />
          </Group>
          <PasswordInput
            label="API key"
            description={secretDescription(editing, 'API key')}
            placeholder={secretPlaceholder(editing)}
            size="md"
            radius="md"
            required={!editing}
            {...form.getInputProps('mailgun.apiKey')}
          />
          <Alert color="blue" variant="light" icon={<IconInfoCircle size={16} />}>
            <Text size="xs">
              Use a <b>sending</b> API key, not the account API key.{' '}
              <Anchor href="https://app.mailgun.com/settings/api_security" target="_blank" size="xs">
                Mailgun API security
              </Anchor>
            </Text>
          </Alert>
        </Stack>
      );

    default:
      return null;
  }
};
