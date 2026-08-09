import React from 'react';
import {
  Stack, Group, Text, TextInput, PasswordInput, Select, Paper, Divider,
  SegmentedControl, Alert, Badge, Anchor,
} from '@mantine/core';
import { IconKey, IconAlertTriangle } from '@tabler/icons-react';

interface OAuthSectionProps {
  form: any;
  /** True when editing a saved provider, so secrets are stored but redacted. */
  editing: boolean;
}

/**
 * SMTP authentication: password or OAuth2.
 *
 * Gmail and Office 365 are retiring password authentication, so a provider
 * pointed at either will eventually stop delivering. The two presets fill in the
 * token endpoint, which is the field people get wrong — Microsoft's is
 * tenant-specific, and a wrong endpoint fails as an authentication error rather
 * than as a configuration one.
 */

const PRESETS: Record<string, { endpoint: string; scope: string; note: string }> = {
  google: {
    endpoint: 'https://oauth2.googleapis.com/token',
    scope: 'https://mail.google.com/',
    note: 'Gmail and Google Workspace.',
  },
  microsoft: {
    endpoint: 'https://login.microsoftonline.com/common/oauth2/v2.0/token',
    scope: 'https://outlook.office.com/SMTP.Send offline_access',
    note: 'Replace "common" with your tenant ID if the app is single-tenant.',
  },
};

export const OAuthSection: React.FC<OAuthSectionProps> = ({ form, editing }) => {
  const mode: string = form.values.smtp?.authMode ?? 'password';

  const applyPreset = (key: string) => {
    const preset = PRESETS[key];
    if (!preset) return;
    form.setFieldValue('smtp.oauth2.tokenEndpoint', preset.endpoint);
    form.setFieldValue('smtp.oauth2.scope', preset.scope);
  };

  const secretHint = (what: string) =>
    editing
      ? `A ${what} is stored. Leave blank to keep it.`
      : 'Stored encrypted and never shown again.';

  return (
    <Paper withBorder p="md" radius="md" mt="md">
      <Stack gap="sm">
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap">
            <IconKey size={18} />
            <div>
              <Text fw={600} size="sm">Authentication</Text>
              <Text size="xs" c="dimmed">
                How this gateway signs in to the SMTP server
              </Text>
            </div>
          </Group>
          <SegmentedControl
            size="xs"
            value={mode}
            onChange={(v) => form.setFieldValue('smtp.authMode', v)}
            data={[
              { value: 'password', label: 'Password' },
              { value: 'oauth2', label: 'OAuth2' },
            ]}
          />
        </Group>

        {mode === 'oauth2' && (
          <>
            <Divider />

            <Group gap="xs">
              <Text size="xs" c="dimmed">Presets:</Text>
              <Badge
                variant="light"
                style={{ cursor: 'pointer' }}
                onClick={() => applyPreset('google')}
              >
                Google
              </Badge>
              <Badge
                variant="light"
                style={{ cursor: 'pointer' }}
                onClick={() => applyPreset('microsoft')}
              >
                Microsoft 365
              </Badge>
            </Group>

            <Select
              label="Mechanism"
              description="XOAUTH2 is what Gmail and Office 365 accept; OAUTHBEARER is offered by fewer servers"
              data={[
                { value: 'XOAUTH2', label: 'XOAUTH2' },
                { value: 'OAUTHBEARER', label: 'OAUTHBEARER (RFC 7628)' },
              ]}
              size="md"
              radius="md"
              {...form.getInputProps('smtp.oauth2.mechanism')}
            />

            <TextInput
              label="Token endpoint"
              description="Where the refresh token is exchanged for an access token"
              placeholder="https://oauth2.googleapis.com/token"
              size="md"
              radius="md"
              {...form.getInputProps('smtp.oauth2.tokenEndpoint')}
            />

            <Group grow align="flex-start">
              <TextInput
                label="Client ID"
                size="md"
                radius="md"
                {...form.getInputProps('smtp.oauth2.clientId')}
              />
              <PasswordInput
                label="Client secret"
                description={secretHint('client secret')}
                placeholder={editing ? '•••••••• stored ••••••••' : ''}
                size="md"
                radius="md"
                {...form.getInputProps('smtp.oauth2.clientSecret')}
              />
            </Group>

            <PasswordInput
              label="Refresh token"
              description={secretHint('refresh token')}
              placeholder={editing ? '•••••••• stored ••••••••' : ''}
              size="md"
              radius="md"
              {...form.getInputProps('smtp.oauth2.refreshToken')}
            />

            <TextInput
              label="Scope"
              description="Optional. Google ignores it on refresh; some providers require it."
              size="md"
              radius="md"
              {...form.getInputProps('smtp.oauth2.scope')}
            />

            <Alert color="orange" variant="light" icon={<IconAlertTriangle size={16} />}>
              <Text size="xs">
                The username above still has to be the mailbox being sent from — OAuth
                authenticates the app, not the sender. A refresh token that is revoked, or
                belongs to a different client, fails every send until this provider is
                reauthorised.{' '}
                <Anchor href="https://developers.google.com/gmail/imap/xoauth2-protocol" target="_blank" size="xs">
                  About XOAUTH2
                </Anchor>
              </Text>
            </Alert>
          </>
        )}
      </Stack>
    </Paper>
  );
};
