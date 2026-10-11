import { sentCopyClient as client } from './client';
import type { MessageInitShape } from '@bufbuild/protobuf';
import {
  SentCopyMailboxSchema,
  SentCopySettingsSchema,
  type SentCopyMailbox,
  type SentCopySettings,
} from '../api/panmail/v1/sent_copy_pb';

export type SentCopySettingsInit = MessageInitShape<typeof SentCopySettingsSchema>;
export type SentCopyMailboxInit = MessageInitShape<typeof SentCopyMailboxSchema>;

export interface SentCopySettingsState {
  // Null, never undefined: react-query treats an undefined result as a broken
  // query function, and a server with nothing stored may send no settings.
  settings: SentCopySettings | null;
  // Without a data key the gateway cannot seal a password, so neither login
  // mode can be saved.
  encryptionAvailable: boolean;
}

export const sentCopyService = {
  getSettings: async (): Promise<SentCopySettingsState> => {
    const res = await client.getSentCopySettings({});
    return { settings: res.settings ?? null, encryptionAvailable: res.encryptionAvailable };
  },

  // Passwords are write-only: an empty one keeps what is stored, so the
  // response — not the request — is what the form re-seeds from.
  updateSettings: async (settings: SentCopySettingsInit): Promise<SentCopySettings | null> => {
    const res = await client.updateSentCopySettings({ settings });
    return res.settings ?? null;
  },

  listMailboxes: async (): Promise<SentCopyMailbox[]> => {
    const res = await client.listSentCopyMailboxes({});
    return res.mailboxes;
  },

  saveMailbox: async (mailbox: SentCopyMailboxInit): Promise<SentCopyMailbox | null> => {
    const res = await client.saveSentCopyMailbox({ mailbox });
    return res.mailbox ?? null;
  },

  deleteMailbox: async (address: string): Promise<void> => {
    await client.deleteSentCopyMailbox({ address });
  },
};
