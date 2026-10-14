import React, { useEffect, useState } from 'react';
import { Anchor, Badge, Group, Paper, Pill, PillsInput, Stack, Text, TextInput, Title } from '@mantine/core';
import {
  IconAlertTriangle,
  IconAt,
  IconCircleCheck,
  IconCircleX,
  IconInfoCircle,
  IconWorld,
} from '@tabler/icons-react';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import {
  addEntries,
  checkSender,
  isDomainEntry,
  pruneCovered,
  sendsMail,
  smtpHints,
  type Feedback,
} from './allowedSenders';

export interface AllowedSendersSectionProps {
  providerType: ProviderType;
  value: string[];
  onChange: (next: string[]) => void;
  /** SMTP only: the account the server signs in as. */
  smtpUsername?: string;
  /** SMTP only: the domain DKIM signs as. */
  dkimDomain?: string;
  error?: string;
}

const TONE_COLOR = {
  error: 'red',
  warning: 'orange',
  info: 'blue',
  success: 'teal',
} as const;

const TONE_ICON = {
  error: IconCircleX,
  warning: IconAlertTriangle,
  info: IconInfoCircle,
  success: IconCircleCheck,
} as const;

const Note: React.FC<{ tone: keyof typeof TONE_COLOR; children: React.ReactNode }> = ({ tone, children }) => {
  const Icon = TONE_ICON[tone];
  return (
    <Group gap={8} wrap="nowrap" align="flex-start">
      <Icon size={16} stroke={2} color={`var(--mantine-color-${TONE_COLOR[tone]}-6)`} style={{ flex: 'none', marginTop: 2 }} />
      <Text size="sm" c={`${TONE_COLOR[tone]}.7`}>{children}</Text>
    </Group>
  );
};

/**
 * The From addresses a provider may send as.
 *
 * The gateway has enforced a per-provider list since anti-spoofing landed,
 * but the form never showed it: it was settable only through the API, and
 * since the form did not send it back, saving a provider from here silently
 * removed whatever had been set. Now the form owns the list and round-trips
 * it.
 *
 * Entries are validated as they are typed with the server's own rules (see
 * allowedSenders.ts), and the checker answers "would this address go
 * through?" without sending anything.
 */
export const AllowedSendersSection: React.FC<AllowedSendersSectionProps> = ({
  providerType,
  value,
  onChange,
  smtpUsername = '',
  dkimDomain = '',
  error,
}) => {
  const [draft, setDraft] = useState('');
  const [feedback, setFeedback] = useState<Feedback[]>([]);
  const [highlight, setHighlight] = useState<string | undefined>();
  const [probe, setProbe] = useState('');

  useEffect(() => {
    if (!highlight) return;
    const t = setTimeout(() => setHighlight(undefined), 1200);
    return () => clearTimeout(t);
  }, [highlight]);

  if (!sendsMail(providerType)) return null;

  const add = (input: string) => {
    if (!input.trim()) return;
    const result = addEntries(value, input);
    if (result.entries.length !== value.length) onChange(result.entries);
    setDraft(result.rejected);
    setFeedback(result.feedback);
    setHighlight(result.highlight);
  };

  const remove = (entry: string) => {
    onChange(value.filter((e) => e !== entry));
    setFeedback([]);
  };

  const prune = (domain: string) => {
    onChange(pruneCovered(value, domain));
    setFeedback([{ tone: 'info', message: `Removed the addresses *@${domain} already covers.` }]);
  };

  const hints = providerType === ProviderType.SMTP ? smtpHints(value, smtpUsername, dkimDomain) : [];
  const hasError = feedback.some((f) => f.tone === 'error');
  const verdict = checkSender(value, probe);

  return (
    <Stack gap="sm">
      <Group justify="space-between" gap="sm">
        <Title order={5}>Allowed senders</Title>
        {value.length > 0 ? (
          <Badge variant="light" color="brand">{value.length} allowed</Badge>
        ) : (
          <Badge variant="light" color="gray">Any sender</Badge>
        )}
      </Group>
      <Text size="sm" c="dimmed">
        The From addresses this provider may send as. A send from any other address is
        refused before it is queued. Leave empty to allow any address.
      </Text>

      <PillsInput
        // On the wrapper: PillsInput hands its id to the field inside.
        id="allowed-senders-input"
        size="md"
        radius="md"
        aria-label="Allowed senders"
        error={error || hasError || undefined}
        onClick={(e) => e.currentTarget.querySelector('input')?.focus()}
      >
        <Pill.Group>
          {value.map((entry) => {
            const domain = isDomainEntry(entry);
            const Icon = domain ? IconWorld : IconAt;
            return (
              <Pill
                key={entry}
                size="md"
                withRemoveButton
                onRemove={() => remove(entry)}
                removeButtonProps={{ 'aria-label': `Remove ${entry}` }}
                title={domain ? `Any address at ${entry.slice(2)}` : 'Only this exact address'}
                data-kind={domain ? 'domain' : 'address'}
                style={{
                  fontFamily: 'var(--mantine-font-family-monospace)',
                  background: domain ? 'var(--mantine-color-brand-light)' : undefined,
                  color: domain ? 'var(--mantine-color-brand-light-color)' : undefined,
                  boxShadow: entry === highlight ? '0 0 0 2px var(--mantine-color-orange-6)' : undefined,
                  transition: 'box-shadow 300ms ease',
                }}
              >
                <Group gap={6} wrap="nowrap" component="span">
                  <Icon size={14} stroke={2} style={{ flex: 'none' }} />
                  <span>{entry}</span>
                </Group>
              </Pill>
            );
          })}
          <PillsInput.Field
            placeholder={value.length ? 'Add another' : 'billing@acme.com or *@acme.com'}
            value={draft}
            aria-describedby="allowed-senders-help"
            onChange={(e) => setDraft(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ',' || e.key === ';') {
                // Enter would otherwise submit the whole provider form.
                e.preventDefault();
                add(draft);
              } else if (e.key === 'Backspace' && !draft && value.length > 0) {
                remove(value[value.length - 1]);
              }
            }}
            onBlur={() => add(draft)}
            onPaste={(e) => {
              const pasted = e.clipboardData.getData('text');
              if (/[\s,;]/.test(pasted.trim())) {
                e.preventDefault();
                add(`${draft} ${pasted}`);
              }
            }}
          />
        </Pill.Group>
      </PillsInput>

      {(error || feedback.length > 0) && (
        <Stack gap={6} aria-live="polite">
          {error && <Note tone="error">{error}</Note>}
          {feedback.map((f, i) => (
            <Note key={i} tone={f.tone}>
              {f.message}
              {f.pruneDomain && (
                <>
                  {' '}
                  <Anchor component="button" type="button" size="sm" onClick={() => prune(f.pruneDomain!)}>
                    Remove them
                  </Anchor>
                </>
              )}
            </Note>
          ))}
        </Stack>
      )}

      <Group justify="space-between" align="flex-start" gap="sm" wrap="nowrap">
        <Text id="allowed-senders-help" size="xs" c="dimmed">
          Press Enter or comma to add. Paste a list to add many at once. *@acme.com allows the
          whole domain but not its subdomains.
        </Text>
        {value.length > 0 && (
          <Anchor
            component="button"
            type="button"
            size="xs"
            style={{ flex: 'none' }}
            onClick={() => {
              onChange([]);
              setFeedback([]);
            }}
          >
            Clear all
          </Anchor>
        )}
      </Group>

      {hints.length > 0 && (
        <Stack gap={6} aria-live="polite">
          {hints.map((h) => (
            <Note key={h.message} tone={h.tone}>
              {h.message}
              {h.add && (
                <>
                  {' '}
                  <Anchor component="button" type="button" size="sm" onClick={() => add(h.add!)}>
                    Add {h.add}
                  </Anchor>
                </>
              )}
            </Note>
          ))}
        </Stack>
      )}

      <Paper withBorder p="sm" radius="md">
        <Stack gap={6}>
          <TextInput
            id="allowed-senders-check"
            label="Check a From address"
            placeholder="Acme Billing <billing@acme.com>"
            size="sm"
            radius="md"
            value={probe}
            onChange={(e) => setProbe(e.currentTarget.value)}
            styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
          />
          <div aria-live="polite">
            {!probe.trim() ? (
              <Text size="sm" c="dimmed">Type an address to see whether this provider would send it.</Text>
            ) : !verdict ? (
              <Text size="sm" c="dimmed">Keep typing: that is not a full address yet.</Text>
            ) : verdict.allowed ? (
              <Note tone="success">
                {verdict.by ? `Allowed by ${verdict.by}.` : 'Allowed. The list is empty, so any sender is allowed.'}
              </Note>
            ) : (
              <Note tone="error">Refused. This provider is not authorized to send as that address.</Note>
            )}
          </div>
        </Stack>
      </Paper>
    </Stack>
  );
};
