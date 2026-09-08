import React from 'react';
import {
  Paper,
  Stack,
  Group,
  Text,
  Title,
  Badge,
  Alert,
  Code,
  Table,
  Select,
  CopyButton,
  ActionIcon,
  Tooltip,
  Loader,
  Button,
  rem,
} from '@mantine/core';
import {
  IconMail,
  IconCopy,
  IconCheck,
  IconAlertTriangle,
  IconInfoCircle,
  IconSettings,
  IconLock,
} from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { settingsService } from '../../../../services/settings';
import { emailProviderService } from '../../../email-providers/services/emailProvider';
import { describeConnection } from './smtpConnection';
import { SmtpSubmissionForm } from './SmtpSubmissionForm';

// A copyable value in the connection table. Everything here is destined for a
// config file somewhere else, so nothing is worth making anyone retype.
const CopyableRow: React.FC<{ label: string; value: string; hint?: React.ReactNode }> = ({
  label,
  value,
  hint,
}) => (
  <Table.Tr>
    <Table.Td w={rem(140)}>
      <Text size="sm" c="dimmed">
        {label}
      </Text>
    </Table.Td>
    <Table.Td>
      <Group gap="xs" wrap="nowrap">
        <Code>{value}</Code>
        <CopyButton value={value}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? 'Copied' : `Copy ${label.toLowerCase()}`} withArrow>
              <ActionIcon
                variant="subtle"
                size="sm"
                color={copied ? 'teal' : 'gray'}
                onClick={copy}
                aria-label={`Copy ${label.toLowerCase()}`}
              >
                {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
              </ActionIcon>
            </Tooltip>
          )}
        </CopyButton>
      </Group>
      {hint && (
        <Text size="xs" c="dimmed" mt={4}>
          {hint}
        </Text>
      )}
    </Table.Td>
  </Table.Tr>
);

/**
 * SmtpIntegrationPanel shows how to point an existing application at panmail
 * over SMTP.
 *
 * Every value it shows comes from the running process rather than from this
 * file, because a panel that guesses at a port sends people to a listener that
 * is not there.
 */
export const SmtpIntegrationPanel: React.FC = () => {
  const { data: submission, isLoading } = useQuery({
    queryKey: ['smtpSubmission'],
    queryFn: settingsService.getSmtpSubmission,
  });

  const { data: settings } = useQuery({
    queryKey: ['systemSettings'],
    queryFn: settingsService.getSettings,
  });

  // Only fetched once SMTP is known to be on: the provider id is the username,
  // and there is nothing to pick between if nobody can connect.
  const { data: providers } = useQuery({
    queryKey: ['emailProviders', 'smtpPanel'],
    queryFn: () => emailProviderService.listProviders(100),
    enabled: submission?.enabled === true,
  });

  const [providerId, setProviderId] = React.useState<string | null>(null);
  const [configuring, setConfiguring] = React.useState(false);

  const connection = describeConnection(submission, settings?.baseUrl);
  const providerOptions = (providers?.providers ?? []).map((p: { id: string; name: string }) => ({
    value: p.id,
    label: p.name,
  }));

  const selected = providerId ?? providerOptions[0]?.value ?? '';

  const editable = submission?.editable ?? false;

  const header = (
    <Group justify="space-between" align="center">
      <Group gap="sm">
        <IconMail size={20} />
        <Title order={4}>SMTP submission</Title>
      </Group>
      <Group gap="xs">
        {isLoading ? (
          <Loader size="xs" />
        ) : (
          <Badge color={connection.enabled ? 'teal' : 'gray'} variant="light">
            {connection.enabled ? 'Enabled' : 'Disabled'}
          </Badge>
        )}
        {!isLoading && editable && (
          <Button
            size="xs"
            variant="light"
            leftSection={<IconSettings size={14} />}
            onClick={() => setConfiguring(true)}
          >
            Configure
          </Button>
        )}
      </Group>
    </Group>
  );

  // Why the panel is read-only, when it is. Flags win over the stored
  // configuration, and a gateway with no data key cannot store a private key at
  // all — both are worth saying rather than showing a button that fails.
  const readOnlyNotice = !editable && submission?.notEditableReason && (
    <Alert icon={<IconInfoCircle size={16} />} color="gray" variant="light">
      <Text size="sm">
        This panel is read-only: {submission.notEditableReason}
      </Text>
    </Alert>
  );

  // Enabling now happens at runtime, so the setting and the socket can
  // disagree. At startup they could not — a listener that would not bind was
  // fatal — which is why this has no equivalent on the flag path.
  const failureNotice = submission?.lastError && (
    <Alert icon={<IconAlertTriangle size={16} />} color="red" variant="light">
      <Text size="sm">
        The listener is not running: {submission.lastError}
      </Text>
    </Alert>
  );

  const configureModal = (
    <SmtpSubmissionForm
      opened={configuring}
      onClose={() => setConfiguring(false)}
      submission={submission}
    />
  );

  if (isLoading) {
    return (
      <Paper p="lg" radius="md" withBorder>
        {header}
      </Paper>
    );
  }

  if (!connection.enabled) {
    return (
      <Paper p="lg" radius="md" withBorder>
        <Stack gap="md">
          {header}
          <Text size="sm" c="dimmed">
            An application that already speaks SMTP can send through panmail without using
            the API. Messages submitted this way go through the same pipeline as the API,
            so rate limits, suppressions and sender checks all still apply.
          </Text>
          {failureNotice}
          {readOnlyNotice}
          {editable ? (
            <Group>
              <Button
                variant="light"
                leftSection={<IconSettings size={16} />}
                onClick={() => setConfiguring(true)}
              >
                Enable SMTP submission
              </Button>
            </Group>
          ) : (
            <Alert icon={<IconInfoCircle size={16} />} color="gray" variant="light">
              <Text size="sm">
                This server is not accepting SMTP submissions. Start panmail with{' '}
                <Code>--smtp-addr</Code> to enable it — for example{' '}
                <Code>--smtp-addr :587 --smtp-tls-cert cert.pem --smtp-tls-key key.pem</Code>.
              </Text>
            </Alert>
          )}
        </Stack>
        {configureModal}
      </Paper>
    );
  }

  return (
    <Paper p="lg" radius="md" withBorder>
      <Stack gap="md">
        {header}

        <Text size="sm" c="dimmed">
          Point an existing application here to send through panmail over SMTP. It uses the
          same pipeline as the API, so rate limits, suppressions and sender checks apply
          unchanged.
        </Text>

        {failureNotice}
        {readOnlyNotice}

        {submission?.certificate && (
          <Group gap="xs">
            <IconLock size={14} />
            <Text size="xs" c="dimmed">
              {submission.certificate.subject} · valid until {submission.certificate.notAfter}
            </Text>
            {submission.certificate.expired && (
              <Badge color="red" variant="light" size="sm">
                Expired
              </Badge>
            )}
          </Group>
        )}

        {connection.insecureAuthAllowed && (
          <Alert icon={<IconAlertTriangle size={16} />} color="orange" variant="light">
            <Text size="sm">
              This listener accepts sign-in without TLS. The password is an API key, which
              carries the tenant&apos;s full sending authority, so use this only where the
              network hop is already private.
            </Text>
          </Alert>
        )}

        <Table verticalSpacing="xs" withRowBorders={false}>
          <Table.Tbody>
            <CopyableRow
              label="Host"
              value={connection.host || 'unknown'}
              hint={
                connection.hostSource === 'baseUrl'
                  ? 'Taken from the base URL in Settings — the listener binds every interface, so it reports no hostname of its own.'
                  : connection.hostSource === 'unknown'
                    ? 'The listener binds every interface and no base URL is set. Use whichever hostname reaches this server.'
                    : undefined
              }
            />
            <CopyableRow label="Port" value={String(connection.port)} />
            <Table.Tr>
              <Table.Td>
                <Text size="sm" c="dimmed">
                  Encryption
                </Text>
              </Table.Td>
              <Table.Td>
                <Badge variant="light" color={connection.encryption === 'STARTTLS' ? 'teal' : 'orange'}>
                  {connection.encryption}
                </Badge>
              </Table.Td>
            </Table.Tr>

            <Table.Tr>
              <Table.Td>
                <Text size="sm" c="dimmed">
                  Username
                </Text>
              </Table.Td>
              <Table.Td>
                <Stack gap="xs">
                  <Select
                    size="xs"
                    placeholder={providerOptions.length ? 'Choose a provider' : 'No providers yet'}
                    data={providerOptions}
                    value={selected || null}
                    onChange={setProviderId}
                    disabled={providerOptions.length === 0}
                    maw={rem(260)}
                    aria-label="Provider for the SMTP username"
                  />
                  {selected && (
                    <Group gap="xs" wrap="nowrap">
                      <Code>{selected}</Code>
                      <CopyButton value={selected}>
                        {({ copied, copy }) => (
                          <Tooltip label={copied ? 'Copied' : 'Copy username'} withArrow>
                            <ActionIcon
                              variant="subtle"
                              size="sm"
                              color={copied ? 'teal' : 'gray'}
                              onClick={copy}
                              aria-label="Copy username"
                            >
                              {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
                            </ActionIcon>
                          </Tooltip>
                        )}
                      </CopyButton>
                    </Group>
                  )}
                  <Text size="xs" c="dimmed">
                    The provider id is the username, because SMTP has nowhere else to say
                    which provider a message goes out through. A message can override it
                    with an <Code>X-Panmail-Provider-Id</Code> header.
                  </Text>
                </Stack>
              </Table.Td>
            </Table.Tr>

            <Table.Tr>
              <Table.Td>
                <Text size="sm" c="dimmed">
                  Password
                </Text>
              </Table.Td>
              <Table.Td>
                <Text size="sm">
                  An API key with the <Code>email:send</Code> scope.
                </Text>
                <Text size="xs" c="dimmed" mt={4}>
                  Create one below. The key is shown once, when it is created.
                </Text>
              </Table.Td>
            </Table.Tr>
          </Table.Tbody>
        </Table>
      </Stack>
      {configureModal}
    </Paper>
  );
};
