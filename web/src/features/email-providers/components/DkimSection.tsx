import React, { useMemo } from 'react';
import {
  Stack, Group, Text, TextInput, Textarea, Paper, Divider, Badge,
  Alert, CopyButton, Tooltip, ActionIcon, Code, Switch,
} from '@mantine/core';
import { IconCopy, IconCheck, IconShieldCheck, IconAlertTriangle, IconWorld } from '@tabler/icons-react';

interface DkimSectionProps {
  form: any;
  /** True when editing a saved provider, so the key is stored but redacted. */
  editing: boolean;
}

/**
 * DKIM configuration.
 *
 * The hard part of DKIM is not entering a key, it is publishing the matching
 * DNS record — a signature verifies only if the receiver can fetch the public
 * key from <selector>._domainkey.<domain>. Getting that wrong produces mail
 * that is signed and still fails, which looks identical to no DKIM at all.
 *
 * So the section is built around the record: enter the three values, and the
 * exact TXT record to publish appears, ready to copy.
 */
export const DkimSection: React.FC<DkimSectionProps> = ({ form, editing }) => {
  const domain: string = form.values.smtp?.dkim?.domain ?? '';
  const selector: string = form.values.smtp?.dkim?.selector ?? '';
  const privateKey: string = form.values.smtp?.dkim?.privateKey ?? '';

  const enabled: boolean = form.values.smtp?.dkimEnabled ?? Boolean(domain || selector || privateKey);

  // Signing is all or nothing: a partial configuration fails at signing time on
  // every send, which stops delivery outright. Unsigned mail is merely
  // distrusted, so half-configured is the worse state and is called out.
  const filled = [domain, selector, editing || privateKey].filter(Boolean).length;
  const complete = filled === 3;
  const partial = enabled && filled > 0 && !complete;

  const recordName = useMemo(
    () => (selector && domain ? `${selector}._domainkey.${domain}` : ''),
    [selector, domain],
  );

  return (
    <Paper withBorder p="md" radius="md" mt="md">
      <Stack gap="sm">
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap">
            <IconShieldCheck size={18} />
            <div>
              <Text fw={600} size="sm">DKIM signing</Text>
              <Text size="xs" c="dimmed">
                Signs every message so receivers can verify it really came from your domain
              </Text>
            </div>
          </Group>
          <Group gap="xs" wrap="nowrap">
            {complete && <Badge color="teal" variant="light">Active</Badge>}
            {partial && <Badge color="orange" variant="light">Incomplete</Badge>}
            <Switch
              aria-label="Enable DKIM signing"
              checked={enabled}
              onChange={(e) => form.setFieldValue('smtp.dkimEnabled', e.currentTarget.checked)}
            />
          </Group>
        </Group>

        {enabled && (
          <>
            <Divider />

            <Group grow align="flex-start">
              <TextInput
                label="Domain"
                description="Should match your From address, or DMARC still fails"
                placeholder="example.com"
                size="md"
                radius="md"
                {...form.getInputProps('smtp.dkim.domain')}
              />
              <TextInput
                label="Selector"
                description="Names which key to publish; any short label"
                placeholder="mail"
                size="md"
                radius="md"
                {...form.getInputProps('smtp.dkim.selector')}
              />
            </Group>

            <Textarea
              label="Private key (PEM)"
              description={
                editing
                  ? 'A key is already stored. Leave this blank to keep it, or paste a new one to replace it.'
                  : 'RSA or Ed25519, including the BEGIN and END lines. Stored encrypted and never shown again.'
              }
              placeholder={editing ? '•••••••• stored ••••••••' : '-----BEGIN PRIVATE KEY-----'}
              minRows={4}
              autosize
              maxRows={8}
              size="md"
              radius="md"
              styles={{ input: { fontFamily: 'monospace', fontSize: 12 } }}
              {...form.getInputProps('smtp.dkim.privateKey')}
            />

            {partial && (
              <Alert color="orange" variant="light" icon={<IconAlertTriangle size={16} />}>
                DKIM needs all three values. Until then messages are sent unsigned —
                they will still be delivered, but receivers trust them less.
              </Alert>
            )}

            {recordName && (
              <Alert color="blue" variant="light" icon={<IconWorld size={16} />}>
                <Stack gap={6}>
                  <Text size="sm" fw={600}>Publish this DNS record</Text>
                  <Text size="xs">
                    A signature only verifies if the receiver can fetch the matching public
                    key. Add a TXT record at:
                  </Text>
                  <Group gap="xs" wrap="nowrap">
                    <Code style={{ flex: 1, wordBreak: 'break-all' }}>{recordName}</Code>
                    <CopyButton value={recordName}>
                      {({ copied, copy }) => (
                        <Tooltip label={copied ? 'Copied' : 'Copy record name'}>
                          <ActionIcon variant="subtle" color={copied ? 'teal' : 'gray'} onClick={copy} aria-label="Copy DNS record name">
                            {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                          </ActionIcon>
                        </Tooltip>
                      )}
                    </CopyButton>
                  </Group>
                  <Text size="xs" c="dimmed">
                    The value is <Code>v=DKIM1; k=rsa; p=&lt;your public key&gt;</Code> — the public
                    half of the key above. Changes can take up to 48 hours to propagate.
                  </Text>
                </Stack>
              </Alert>
            )}
          </>
        )}
      </Stack>
    </Paper>
  );
};
