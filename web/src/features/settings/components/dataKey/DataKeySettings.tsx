import React from 'react';
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Checkbox,
  Code,
  CopyButton,
  Group,
  Loader,
  Modal,
  Paper,
  Stack,
  Text,
  TextInput,
  Tooltip,
} from '@mantine/core';
import {
  IconAlertTriangle,
  IconCheck,
  IconCopy,
  IconInfoCircle,
  IconKey,
  IconRefresh,
} from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { notifications } from '@mantine/notifications';
import { settingsService } from '../../../../services/settings';
import { DATA_KEY_PATTERN, generateDataKey } from './dataKey';


/**
 * DataKeySettings shows which key stored credentials are encrypted with and
 * lets a super admin replace it, either with a key they paste in or with one
 * generated here.
 *
 * The key is generated in the browser rather than by the server so it can be
 * copied and saved before anything changes: a key nobody kept is a backup that
 * restores to unreadable credentials. The server never sends a key back.
 */
export const DataKeySettings: React.FC = () => {
  const queryClient = useQueryClient();
  const { data: dataKey, isLoading, isError } = useQuery({
    queryKey: ['dataKey'],
    queryFn: settingsService.getDataKey,
  });

  const [opened, setOpened] = React.useState(false);
  const [key, setKey] = React.useState('');
  const [saved, setSaved] = React.useState(false);

  const close = () => {
    setOpened(false);
    setKey('');
    setSaved(false);
  };

  const mutation = useMutation({
    mutationFn: (value: string) => settingsService.changeDataKey(value),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['dataKey'] });
      close();
      notifications.show({
        title: 'Encryption key changed',
        message: `${res.rotated} stored credential(s) re-encrypted under key ${res.dataKey?.keyId ?? ''}.`,
        color: 'green',
      });
    },
    onError: (error: Error) => {
      // The dialog stays open with the key still in it, so nothing typed or
      // generated is lost when the change is refused.
      queryClient.invalidateQueries({ queryKey: ['dataKey'] });
      notifications.show({
        title: 'Encryption key not changed',
        message: error.message || 'Failed to change the encryption key',
        color: 'red',
      });
    },
  });

  const trimmed = key.trim();
  const valid = DATA_KEY_PATTERN.test(trimmed);
  const managedByEnv = dataKey?.managedByEnvironment === true;

  return (
    <Paper withBorder p="xl" radius="md">
      <Stack gap="md">
        <Group gap="xs">
          <IconKey size={18} color="var(--mantine-color-brand-6)" />
          <Text fw={700} size="sm" tt="uppercase">
            Encryption Key
          </Text>
        </Group>
        <Text size="sm" c="dimmed">
          Provider passwords, webhook secrets, mailbox logins and the SMTP TLS key are
          encrypted with this key. Keep a copy somewhere safe: without it, a restored backup
          cannot read any of them.
        </Text>

        {isLoading ? <Loader size="sm" /> : null}
        {isError ? (
          <Alert icon={<IconAlertTriangle size={16} />} color="red" variant="light">
            The encryption key status could not be loaded.
          </Alert>
        ) : null}

        {dataKey ? (
          <Group gap="sm">
            <Text size="sm">Key ID</Text>
            <Code>{dataKey.keyId}</Code>
            <Badge variant="light" color={managedByEnv ? 'gray' : 'blue'}>
              {managedByEnv ? 'Set by PANMAIL_SECRET_KEY' : 'Stored in the config file'}
            </Badge>
          </Group>
        ) : null}

        {dataKey && dataKey.retiredKeys > 0 ? (
          <Alert icon={<IconAlertTriangle size={16} />} color="orange" variant="light" title="A key change did not finish">
            Some credentials may still be on an older key, which is kept for reading them.
            Changing the key again finishes the move.
          </Alert>
        ) : null}

        {managedByEnv ? (
          <Alert icon={<IconInfoCircle size={16} />} color="blue" variant="light">
            This key comes from the <Code>PANMAIL_SECRET_KEY</Code> environment variable, which
            always wins at startup, so it can only be changed there. Move the old key into{' '}
            <Code>PANMAIL_SECRET_KEYS_RETIRED</Code>, restart, then run{' '}
            <Code>panmail rotate-secrets</Code>.
          </Alert>
        ) : (
          <Group>
            <Button
              variant="light"
              leftSection={<IconKey size={16} />}
              onClick={() => setOpened(true)}
              disabled={!dataKey}
            >
              Change encryption key
            </Button>
          </Group>
        )}
      </Stack>

      <Modal opened={opened} onClose={close} title={<Text fw={700}>Change encryption key</Text>} centered size="lg">
        <Stack gap="md">
          <Text size="sm">
            Paste a key of 64 hex characters, or generate one. Every stored credential is
            re-encrypted under it, and the old key stops working once that finishes.
          </Text>

          <TextInput
            label="New key"
            placeholder="64 hex characters"
            value={key}
            onChange={(e) => {
              setKey(e.currentTarget.value);
              setSaved(false);
            }}
            error={trimmed !== '' && !valid ? 'Must be exactly 64 hex characters (32 bytes)' : undefined}
            styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
            autoComplete="off"
            spellCheck={false}
            rightSectionWidth={70}
            rightSection={
              <Group gap={4} wrap="nowrap">
                <Tooltip label="Generate a key" withArrow>
                  <ActionIcon
                    variant="subtle"
                    aria-label="Generate a key"
                    onClick={() => {
                      setKey(generateDataKey());
                      setSaved(false);
                    }}
                  >
                    <IconRefresh size={16} />
                  </ActionIcon>
                </Tooltip>
                <CopyButton value={trimmed}>
                  {({ copied, copy }) => (
                    <Tooltip label={copied ? 'Copied' : 'Copy key'} withArrow>
                      <ActionIcon
                        variant="subtle"
                        color={copied ? 'teal' : 'gray'}
                        aria-label="Copy key"
                        onClick={copy}
                        disabled={!valid}
                      >
                        {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                      </ActionIcon>
                    </Tooltip>
                  )}
                </CopyButton>
              </Group>
            }
          />

          <Alert icon={<IconInfoCircle size={16} />} color="blue" variant="light">
            The key is saved to the config file of this instance. If several instances share
            this database, set <Code>PANMAIL_SECRET_KEY</Code> on all of them instead.
          </Alert>

          <Checkbox
            label="I have saved this key somewhere safe"
            checked={saved}
            onChange={(e) => setSaved(e.currentTarget.checked)}
            disabled={!valid}
          />

          <Box>
            <Group justify="flex-end">
              <Button variant="default" onClick={close}>
                Cancel
              </Button>
              <Button
                color="orange"
                onClick={() => mutation.mutate(trimmed)}
                loading={mutation.isPending}
                disabled={!valid || !saved}
              >
                Change key
              </Button>
            </Group>
          </Box>
        </Stack>
      </Modal>
    </Paper>
  );
};
