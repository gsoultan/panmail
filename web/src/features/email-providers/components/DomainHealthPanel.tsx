import React from 'react';
import { useMutation } from '@tanstack/react-query';
import {
  Stack, Group, Text, Button, Paper, Badge, Code, Alert, Tooltip, ThemeIcon, Divider,
} from '@mantine/core';
import {
  IconShieldCheck, IconShieldX, IconAlertTriangle, IconRefresh, IconWorldSearch, IconInfoCircle,
} from '@tabler/icons-react';
import { DkimKeyMatch } from '../../../api/panmail/v1/email_provider_service_pb';
import { emailProviderService } from '../services/emailProvider';
import { summarise, type CheckStatus, type HealthSummary } from './domainHealth';

interface DomainHealthPanelProps {
  /** Present once the provider is saved; required for the key comparison. */
  providerId?: string;
  /** Falls back to this when the provider has no DKIM domain of its own. */
  domain?: string;
  selector?: string;
}

/**
 * Shows whether the rest of the world is set up to believe this sender.
 *
 * Testing the connection proves the server accepts a message. Nothing so far
 * showed whether recipients will accept the message it then sends, which is
 * decided by DNS the sender does not control. The DKIM row is the reason this
 * exists: signing is configured by pasting a private key, and a key whose
 * public half was never published — or was published from a different pair —
 * makes every message fail verification while everything else reads healthy.
 */
export const DomainHealthPanel: React.FC<DomainHealthPanelProps> = ({ providerId, domain, selector }) => {
  const check = useMutation({
    mutationFn: () =>
      emailProviderService.checkDomainHealth({
        providerId,
        domain,
        selectors: selector ? [selector] : [],
      }),
  });

  const summary = check.data ? summarise(check.data) : undefined;

  return (
    <Paper withBorder p="md" radius="md">
      <Stack gap="sm">
        <Group justify="space-between" wrap="nowrap" align="flex-start">
          <div>
            <Group gap="xs">
              <Text fw={600} size="sm">Domain health</Text>
              {summary && <OverallBadge summary={summary} />}
            </Group>
            <Text size="xs" c="dimmed">
              Whether SPF, DKIM and DMARC are published for the sending domain. Testing the
              connection only proves the server accepts mail — this is what recipients check.
            </Text>
          </div>
          <Button
            size="compact-sm"
            variant="light"
            leftSection={check.data ? <IconRefresh size={14} /> : <IconWorldSearch size={14} />}
            onClick={() => check.mutate()}
            loading={check.isPending}
            disabled={!providerId && !domain}
          >
            {check.data ? 'Re-check' : 'Check DNS'}
          </Button>
        </Group>

        {!providerId && !domain && (
          <Text size="xs" c="dimmed">
            Save the provider, or fill in a DKIM domain, to check its DNS.
          </Text>
        )}

        {check.isError && (
          <Alert color="red" variant="light" icon={<IconAlertTriangle size={16} />} p="xs">
            <Text size="xs">{(check.error as Error).message}</Text>
          </Alert>
        )}

        {summary && (
          <>
            <Divider />
            <Stack gap={6}>
              {summary.rows.map((row) => (
                <CheckRow key={row.label} {...row} />
              ))}
            </Stack>
          </>
        )}
      </Stack>
    </Paper>
  );
};

const STATUS_COLOR: Record<CheckStatus, string> = {
  pass: 'teal',
  warn: 'yellow',
  fail: 'red',
  unknown: 'gray',
};

const OverallBadge: React.FC<{ summary: HealthSummary }> = ({ summary }) => (
  <Badge size="sm" variant="light" color={STATUS_COLOR[summary.overall]}>
    {summary.overall === 'pass' ? 'Healthy' : summary.overall === 'fail' ? 'Action needed' : 'Check'}
  </Badge>
);

interface CheckRowProps {
  label: string;
  status: CheckStatus;
  detail: string;
  record?: string;
}

const CheckRow: React.FC<CheckRowProps> = ({ label, status, detail, record }) => (
  <Group gap="xs" wrap="nowrap" align="flex-start">
    <ThemeIcon size="sm" radius="xl" variant="light" color={STATUS_COLOR[status]} mt={2}>
      {status === 'pass' ? <IconShieldCheck size={12} /> : status === 'fail' ? <IconShieldX size={12} /> : <IconInfoCircle size={12} />}
    </ThemeIcon>
    <div style={{ flex: 1, minWidth: 0 }}>
      <Group gap={6} wrap="nowrap">
        <Text size="sm" fw={500}>{label}</Text>
        {record && (
          // The published record, so it can be compared against what was meant
          // without leaving the page.
          <Tooltip label={record} multiline w={420} withArrow>
            <Code style={{ fontSize: 10, maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {record}
            </Code>
          </Tooltip>
        )}
      </Group>
      <Text size="xs" c={status === 'fail' ? 'red' : 'dimmed'}>{detail}</Text>
    </div>
  </Group>
);

export { DkimKeyMatch };
