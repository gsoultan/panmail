import { settingsClient as client } from './client';
import { SystemSettings, SmtpSubmission } from '../api/panmail/v1/system_settings_pb';

export const settingsService = {
  getSettings: async () => {
    const res = await client.getSettings({});
    return res.settings;
  },

  // The SMTP listener is read-only process state that arrives on the same
  // response as the settings. It is fetched through its own accessor so the
  // settings form, which writes what it reads, never sees a field it cannot
  // save.
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
};
