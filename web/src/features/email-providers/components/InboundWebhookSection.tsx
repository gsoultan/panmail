import React from 'react';
import {
  ActionIcon,
  Alert,
  Code,
  CopyButton,
  Divider,
  Group,
  PasswordInput,
  Stack,
  Text,
  Title,
  Tooltip,
} from '@mantine/core';
import { IconCheck, IconCopy, IconInfoCircle } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import { settingsService } from '../../../services/settings';
import { useAuthStore } from '../../../store/authStore';
import {
  canShowWebhookUrl,
  inboundWebhookUrl,
  secretFieldFor,
  webhookTypeFor,
} from './inboundWebhook';

export interface InboundWebhookSectionProps {
  providerType: ProviderType;
  /** Absent while the provider is being created; the URL contains its id. */
  providerId?: string;
  value: string;
  onChange: (next: string) => void;
  error?: string;
}

/**
 * Configures the webhook a provider posts delivery events to.
 *
 * Until this existed the secret was settable only by calling UpdateEmailProvider
 * by hand, so in practice nobody configured one — and an unconfigured secret
 * means every event the provider sends is refused. The send worked and the
 * feedback never arrived.
 */
export const InboundWebhookSection: React.FC<InboundWebhookSectionProps> = ({
  providerType,
  providerId,
  value,
  onChange,
  error,
}) => {
  const webhookType = webhookTypeFor(providerType);
  const secretField = secretFieldFor(providerType);
  const selectedTenantID = useAuthStore((s) => s.selectedTenantID);

  // Only fetched for a provider that has inbound events, so the create form
  // for an SMTP provider does not pull settings it will not display.
  const { data: settings } = useQuery({
    queryKey: ['systemSettings'],
    queryFn: settingsService.getSettings,
    enabled: webhookType !== null,
  });

  if (!webhookType || !secretField) return null;

  const url = inboundWebhookUrl({
    baseUrl: settings?.baseUrl ?? '',
    tenantId: selectedTenantID ?? '',
    providerId: providerId ?? '',
    type: webhookType,
  });

  return (
    <Stack gap="sm">
      <Divider my="xs" />
      <Title order={5}>Delivery events</Title>
      <Text size="sm" c="dimmed">
        Point this provider&rsquo;s event webhook here so bounces and complaints reach
        Panmail. Without it a send is one-way: mail goes out and nothing reports that an
        address is dead.
      </Text>

      {canShowWebhookUrl(providerId) ? (
        url ? (
          <Group gap="xs" wrap="nowrap">
            <Code style={{ flex: 1, overflowWrap: 'anywhere' }}>{url}</Code>
            <CopyButton value={url} timeout={2000}>
              {({ copied, copy }) => (
                <Tooltip label={copied ? 'Copied' : 'Copy'} withArrow>
                  <ActionIcon variant="subtle" color={copied ? 'teal' : 'gray'} onClick={copy}>
                    {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                  </ActionIcon>
                </Tooltip>
              )}
            </CopyButton>
          </Group>
        ) : (
          <Alert icon={<IconInfoCircle size={16} />} color="yellow" variant="light">
            Set a base URL on the Settings page first. The provider has to reach Panmail
            from the internet, so the address the console is open on is not enough.
          </Alert>
        )
      ) : (
        <Alert icon={<IconInfoCircle size={16} />} color="blue" variant="light">
          Save the provider to see its webhook URL &mdash; the address contains the id it
          is about to be given.
        </Alert>
      )}

      <PasswordInput
        label={secretField.label}
        description={secretField.description}
        placeholder={
          providerId ? 'Leave blank to keep the current value' : secretField.placeholder
        }
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        error={error}
      />

      {providerId && (
        <Text size="xs" c="dimmed">
          Stored values are never sent back to the browser, so this field is always blank
          when you open a saved provider. Leaving it blank keeps what is already stored.
        </Text>
      )}
    </Stack>
  );
};
