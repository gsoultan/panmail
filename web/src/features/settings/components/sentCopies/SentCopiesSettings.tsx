import React from 'react';
import {
  Alert,
  Badge,
  Button,
  Divider,
  Group,
  Loader,
  NumberInput,
  Paper,
  PasswordInput,
  Radio,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { IconAlertTriangle, IconDeviceFloppy, IconMailForward } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { notifications } from '@mantine/notifications';
import { SentCopyMode } from '../../../../api/panmail/v1/sent_copy_pb';
import { sentCopyService } from '../../../../services/sentCopies';
import { useAdaptedForm } from '../../../../lib/form/useAdaptedForm';
import {
  DEFAULT_SENT_FOLDER,
  DEFAULT_SEPARATOR,
  buildSentCopyUpdate,
  defaultImapPort,
  initialSentCopyValues,
  normalizeMode,
  usesImap,
  validateSentCopy,
  type SentCopyFormValues,
} from './sentCopyRules';
import { SentCopyMailboxes } from './SentCopyMailboxes';

const SENT_COPY_SETTINGS_KEY = ['sentCopySettings'];

interface ModeOption {
  mode: SentCopyMode;
  label: string;
  description: string;
  /** Stores a password, so it needs the gateway's data key. */
  needsEncryption: boolean;
}

const MODE_OPTIONS: ModeOption[] = [
  {
    mode: SentCopyMode.NONE,
    label: 'None',
    description: 'panmail doesn’t save copies. Messages sent through it won’t appear in anyone’s Sent folder.',
    needsEncryption: false,
  },
  {
    mode: SentCopyMode.SHARED_LOGIN,
    label: 'Shared admin login',
    description:
      'One master IMAP account files a copy in each sender’s Sent folder. No personal passwords are stored. The mail server must support master users (e.g. Dovecot).',
    needsEncryption: true,
  },
  {
    mode: SentCopyMode.PER_USER_LOGIN,
    label: 'Per-user login',
    description: 'Each sender’s mailbox login is stored (encrypted) and used to file the copy in their Sent folder.',
    needsEncryption: true,
  },
  {
    mode: SentCopyMode.BCC_SENDER,
    label: 'Bcc to sender',
    description: 'panmail emails a copy to the sender. It lands in their Inbox unless their mail server files it.',
    needsEncryption: false,
  },
];

const labelFor = (mode: SentCopyMode) =>
  MODE_OPTIONS.find((option) => option.mode === normalizeMode(mode))?.label ?? 'None';

/**
 * SentCopiesSettings decides how a copy of each sent message gets back to the
 * person who sent it, so mail sent through panmail from a mail client still
 * shows up in their Sent folder.
 *
 * Passwords are write-only: the server never returns one, so the fields start
 * empty and an empty field keeps what is stored.
 */
export const SentCopiesSettings: React.FC = () => {
  const queryClient = useQueryClient();
  const { data, isLoading, isError, error } = useQuery({
    queryKey: SENT_COPY_SETTINGS_KEY,
    queryFn: sentCopyService.getSettings,
  });

  const settings = data?.settings ?? null;
  const encryptionAvailable = data?.encryptionAvailable ?? true;
  const masterPasswordSet = settings?.masterPasswordSet ?? false;
  const storedMode = normalizeMode(settings?.mode);

  const validate = React.useCallback(
    (values: SentCopyFormValues) =>
      validateSentCopy(values, { masterPasswordSet, encryptionAvailable }),
    [masterPasswordSet, encryptionAvailable],
  );

  const form = useAdaptedForm<SentCopyFormValues>({
    initialValues: initialSentCopyValues(null),
    validate,
  });

  // Depends on setValues rather than `form`, whose identity changes every
  // render; re-seeding on every render would reset whatever is being typed.
  const { setValues } = form;
  React.useEffect(() => {
    if (data) setValues(initialSentCopyValues(data.settings));
  }, [data, setValues]);

  const save = useMutation({
    mutationFn: (values: SentCopyFormValues) => sentCopyService.updateSettings(buildSentCopyUpdate(values)),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: SENT_COPY_SETTINGS_KEY });
      notifications.show({
        title: 'Sent copies updated',
        message: 'Sent-copy settings have been saved.',
        color: 'green',
      });
    },
    onError: (err: Error) => {
      notifications.show({
        title: 'Error',
        message: err.message || 'Failed to update sent-copy settings',
        color: 'red',
      });
    },
  });

  const { values } = form;
  const mode = normalizeMode(values.mode);
  const showImap = usesImap(mode);

  const header = (
    <Group justify="space-between" align="center">
      <Group gap="sm">
        <IconMailForward size={20} />
        <Title order={4}>Sent copies</Title>
      </Group>
      {isLoading ? (
        <Loader size="xs" />
      ) : (
        <Badge color={storedMode === SentCopyMode.NONE ? 'gray' : 'teal'} variant="light">
          {labelFor(storedMode)}
        </Badge>
      )}
    </Group>
  );

  if (isLoading) {
    return (
      <Paper p="lg" radius="md" withBorder>
        {header}
      </Paper>
    );
  }

  if (isError) {
    return (
      <Paper p="lg" radius="md" withBorder>
        <Stack gap="md">
          {header}
          <Alert icon={<IconAlertTriangle size={16} />} color="red" variant="light">
            <Text size="sm">Could not load sent-copy settings: {(error as Error).message}</Text>
          </Alert>
        </Stack>
      </Paper>
    );
  }

  return (
    <Paper p="lg" radius="md" withBorder>
      <form onSubmit={form.onSubmit((submitted) => save.mutate(submitted))}>
        <Stack gap="md">
          {header}

          <Text size="sm" c="dimmed">
            Mail sent through panmail doesn’t pass through the sender’s mailbox, so it won’t
            appear in their Sent folder unless panmail puts a copy there.
          </Text>

          {!encryptionAvailable && (
            <Alert icon={<IconAlertTriangle size={16} />} color="orange" variant="light">
              <Text size="sm">
                This gateway has no data key to encrypt passwords with, so the two login modes
                can’t be saved. Configure a data key to use them.
              </Text>
            </Alert>
          )}

          <Radio.Group
            label="How copies reach the sender"
            value={String(mode)}
            onChange={(value) => form.setFieldValue('mode', Number(value) as SentCopyMode)}
            error={form.errors.mode}
          >
            <Stack gap="sm" mt="xs">
              {MODE_OPTIONS.map((option) => (
                <Radio
                  key={option.mode}
                  value={String(option.mode)}
                  label={option.label}
                  description={option.description}
                  disabled={option.needsEncryption && !encryptionAvailable && mode !== option.mode}
                />
              ))}
            </Stack>
          </Radio.Group>

          {showImap && (
            <>
              <Divider label="IMAP server" labelPosition="left" />
              <Group grow align="flex-start">
                <TextInput
                  label="Host"
                  placeholder="imap.example.com"
                  {...form.getInputProps('imapHost')}
                />
                <NumberInput
                  label="Port"
                  placeholder={String(defaultImapPort(values.imapUseSsl))}
                  min={1}
                  max={65535}
                  allowDecimal={false}
                  allowNegative={false}
                  {...form.getInputProps('imapPort')}
                />
              </Group>
              <Switch
                label="Use SSL/TLS (implicit)"
                description={
                  values.imapUseSsl
                    ? 'Connects with TLS from the start, usually on port 993.'
                    : 'Off means STARTTLS is required, usually on port 143. panmail never logs in unencrypted.'
                }
                {...form.getInputProps('imapUseSsl', { type: 'checkbox' })}
              />
              <TextInput
                label="Sent folder"
                placeholder={DEFAULT_SENT_FOLDER}
                description="The folder copies are filed in."
                {...form.getInputProps('sentFolder')}
              />
            </>
          )}

          {mode === SentCopyMode.SHARED_LOGIN && (
            <>
              <Divider label="Master user" labelPosition="left" />
              <TextInput
                label="Master username"
                placeholder="admin"
                {...form.getInputProps('masterUsername')}
              />
              <PasswordInput
                label="Master password"
                placeholder={masterPasswordSet ? 'Stored — leave blank to keep' : ''}
                description="Encrypted before it is stored and never shown again."
                autoComplete="new-password"
                {...form.getInputProps('masterPassword')}
              />
              <TextInput
                label="Separator"
                placeholder={DEFAULT_SEPARATOR}
                description={`panmail logs in as <sender>${values.masterSeparator || DEFAULT_SEPARATOR}${values.masterUsername || '<master>'}.`}
                {...form.getInputProps('masterSeparator')}
              />
            </>
          )}

          <Group justify="flex-end">
            <Button
              type="submit"
              leftSection={<IconDeviceFloppy size={16} />}
              loading={save.isPending}
            >
              Save sent-copy settings
            </Button>
          </Group>

        </Stack>
      </form>

      {/* Outside the form: mailbox logins save on their own, and the editor
          modal's events would otherwise bubble into this form's submit. */}
      {mode === SentCopyMode.PER_USER_LOGIN && (
        <Stack gap="md" mt="md">
          <Divider label="Mailbox logins" labelPosition="left" />
          <SentCopyMailboxes encryptionAvailable={encryptionAvailable} />
        </Stack>
      )}
    </Paper>
  );
};
