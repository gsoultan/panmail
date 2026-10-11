import React from 'react';
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Group,
  Modal,
  PasswordInput,
  Stack,
  Table,
  Text,
  TextInput,
  Tooltip,
} from '@mantine/core';
import { IconAlertTriangle, IconPencil, IconPlus, IconTrash } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { notifications } from '@mantine/notifications';
import type { SentCopyMailbox } from '../../../../api/panmail/v1/sent_copy_pb';
import { sentCopyService } from '../../../../services/sentCopies';
import {
  buildMailboxSave,
  hasErrors,
  initialMailboxValues,
  validateMailbox,
  type MailboxFormValues,
} from './sentCopyRules';

const SENT_COPY_MAILBOXES_KEY = ['sentCopyMailboxes'];

interface EditorState {
  /** The row being edited, or null when adding a new one. */
  mailbox: SentCopyMailbox | null;
}

/**
 * The stored mailbox logins for per-user mode. Each row saves on its own,
 * independently of the mode and server settings above it.
 */
export const SentCopyMailboxes: React.FC<{ encryptionAvailable: boolean }> = ({
  encryptionAvailable,
}) => {
  const queryClient = useQueryClient();
  const { data: mailboxes = [], isLoading, isError, error } = useQuery({
    queryKey: SENT_COPY_MAILBOXES_KEY,
    queryFn: sentCopyService.listMailboxes,
  });

  const [editor, setEditor] = React.useState<EditorState | null>(null);

  const remove = useMutation({
    mutationFn: (address: string) => sentCopyService.deleteMailbox(address),
    onSuccess: async (_, address) => {
      await queryClient.invalidateQueries({ queryKey: SENT_COPY_MAILBOXES_KEY });
      notifications.show({
        title: 'Mailbox removed',
        message: `${address} will no longer get sent copies.`,
        color: 'green',
      });
    },
    onError: (err: Error) => {
      notifications.show({
        title: 'Error',
        message: err.message || 'Failed to remove the mailbox',
        color: 'red',
      });
    },
  });

  const confirmRemove = (address: string) => {
    if (window.confirm(`Remove the stored login for ${address}? Copies of its sent mail will stop.`)) {
      remove.mutate(address);
    }
  };

  return (
    <Stack gap="sm">
      <Group justify="space-between" align="flex-start">
        <Text size="sm" c="dimmed" style={{ flex: 1 }}>
          A copy is filed only for senders listed here. Changes to this list are saved immediately.
        </Text>
        <Button
          size="xs"
          variant="light"
          leftSection={<IconPlus size={14} />}
          onClick={() => setEditor({ mailbox: null })}
          disabled={!encryptionAvailable}
        >
          Add mailbox
        </Button>
      </Group>

      {isError && (
        <Alert icon={<IconAlertTriangle size={16} />} color="red" variant="light">
          <Text size="sm">Could not load mailboxes: {(error as Error).message}</Text>
        </Alert>
      )}

      <Table verticalSpacing="xs">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Address</Table.Th>
            <Table.Th>Login username</Table.Th>
            <Table.Th>Password</Table.Th>
            <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {!isLoading && mailboxes.length === 0 ? (
            <Table.Tr>
              <Table.Td colSpan={4}>
                <Text size="sm" c="dimmed" ta="center">
                  No mailboxes yet. Add one for each sender whose copies should be filed.
                </Text>
              </Table.Td>
            </Table.Tr>
          ) : (
            mailboxes.map((mailbox) => (
              <Table.Tr key={mailbox.address}>
                <Table.Td>
                  <Text size="sm">{mailbox.address}</Text>
                </Table.Td>
                <Table.Td>
                  <Text size="sm" c={mailbox.username ? undefined : 'dimmed'}>
                    {mailbox.username || 'Same as address'}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <Badge size="sm" variant="light" color={mailbox.passwordSet ? 'teal' : 'orange'}>
                    {mailbox.passwordSet ? 'Stored' : 'Not stored'}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  <Group gap={4} justify="flex-end" wrap="nowrap">
                    <Tooltip label="Edit" withArrow>
                      <ActionIcon
                        variant="subtle"
                        aria-label={`Edit ${mailbox.address}`}
                        onClick={() => setEditor({ mailbox })}
                        disabled={!encryptionAvailable}
                      >
                        <IconPencil size={16} />
                      </ActionIcon>
                    </Tooltip>
                    <Tooltip label="Remove" withArrow>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={`Remove ${mailbox.address}`}
                        onClick={() => confirmRemove(mailbox.address)}
                        loading={remove.isPending && remove.variables === mailbox.address}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Tooltip>
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))
          )}
        </Table.Tbody>
      </Table>

      <MailboxEditor
        opened={editor !== null}
        mailbox={editor?.mailbox ?? null}
        onClose={() => setEditor(null)}
      />
    </Stack>
  );
};

interface MailboxEditorProps {
  opened: boolean;
  mailbox: SentCopyMailbox | null;
  onClose: () => void;
}

const MailboxEditor: React.FC<MailboxEditorProps> = ({ opened, mailbox, onClose }) => {
  const queryClient = useQueryClient();
  const [values, setValues] = React.useState<MailboxFormValues>(() => initialMailboxValues(mailbox));
  const [attempted, setAttempted] = React.useState(false);

  // Re-seed whenever the dialog opens, so a cancelled edit never leaks into
  // the next one.
  React.useEffect(() => {
    if (opened) {
      setValues(initialMailboxValues(mailbox));
      setAttempted(false);
    }
  }, [opened, mailbox]);

  const editing = mailbox !== null;
  const passwordSet = mailbox?.passwordSet ?? false;
  const errors = validateMailbox(values, passwordSet);

  const save = useMutation({
    mutationFn: () => sentCopyService.saveMailbox(buildMailboxSave(values)),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: SENT_COPY_MAILBOXES_KEY });
      notifications.show({
        title: 'Mailbox saved',
        message: `${values.address.trim()} will get copies of its sent mail.`,
        color: 'green',
      });
      onClose();
    },
    onError: (err: Error) => {
      notifications.show({
        title: 'Error',
        message: err.message || 'Failed to save the mailbox',
        color: 'red',
      });
    },
  });

  const update = (key: keyof MailboxFormValues, value: string) =>
    setValues((current) => ({ ...current, [key]: value }));

  const submit = () => {
    setAttempted(true);
    if (!hasErrors(errors)) save.mutate();
  };

  return (
    <Modal opened={opened} onClose={onClose} title={editing ? 'Edit mailbox' : 'Add mailbox'}>
      <Stack gap="md">
        <TextInput
          label="Address"
          placeholder="alice@example.com"
          value={values.address}
          onChange={(event) => update('address', event.currentTarget.value)}
          // The address is the row's key; changing it is a new row.
          disabled={editing}
          error={attempted ? errors.address : undefined}
          data-autofocus
        />
        <TextInput
          label="Login username"
          placeholder="same as address"
          value={values.username}
          onChange={(event) => update('username', event.currentTarget.value)}
        />
        <PasswordInput
          label="Password"
          placeholder={passwordSet ? 'Stored — leave blank to keep' : ''}
          description="Encrypted before it is stored and never shown again."
          autoComplete="new-password"
          value={values.password}
          onChange={(event) => update('password', event.currentTarget.value)}
          error={attempted ? errors.password : undefined}
        />
        <Group justify="flex-end">
          <Button variant="subtle" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} loading={save.isPending}>
            Save mailbox
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
