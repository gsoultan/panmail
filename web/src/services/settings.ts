import { settingsClient as client } from './client';
import type { MessageInitShape } from '@bufbuild/protobuf';
import {
  SystemSettings,
  SmtpSubmission,
  SmtpSubmissionConfigSchema,
} from '../api/panmail/v1/system_settings_pb';

export type SmtpSubmissionConfigInit = MessageInitShape<typeof SmtpSubmissionConfigSchema>;

export const settingsService = {
  getSettings: async () => {
    const res = await client.getSettings({});
    return res.settings;
  },

  // The SMTP listener arrives on the same response as the settings but is not
  // part of them. It is fetched through its own accessor so the settings form,
  // which writes what it reads, never sees a field it cannot save — the
  // listener is written through updateSmtpSubmission instead, and its TLS
  // fields are write-only.
  // Null, never undefined. The block is optional on the wire, so a server that
  // predates it sends nothing — and react-query treats an undefined result as a
  // broken query function rather than as "no listener".
  getSmtpSubmission: async (): Promise<SmtpSubmission | null> => {
    const res = await client.getSettings({});
    return res.smtpSubmission ?? null;
  },

  updateSettings: async (settings: SystemSettings) => {
    const res = await client.updateSettings({ settings });
    return res.settings;
  },

  // Returns the listener as the server sees it afterwards, not the config that
  // was sent: a change can be stored and still fail to bind, and the response
  // is the only place that says so.
  updateSmtpSubmission: async (config: SmtpSubmissionConfigInit): Promise<SmtpSubmission | null> => {
    const res = await client.updateSmtpSubmission({ config });
    return res.smtpSubmission ?? null;
  },
};
