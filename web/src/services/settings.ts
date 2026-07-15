import { settingsClient as client } from './client';
import { SystemSettings } from '../api/panmail/v1/system_settings_pb';

export const settingsService = {
  getSettings: async () => {
    const res = await client.getSettings({});
    return res.settings;
  },
  updateSettings: async (settings: SystemSettings) => {
    const res = await client.updateSettings({ settings });
    return res.settings;
  },
};
