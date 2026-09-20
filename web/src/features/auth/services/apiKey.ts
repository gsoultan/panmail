import { apiKeyClient } from "../../../services/client";

export const apiKeyService = {
  /**
   * Mints a key.
   *
   * `scopes` is the resolved flat grant, not the picker's include/exclude tree
   * — the server stores exactly what it is handed. Sending an empty list is
   * meaningful: NormalizeScopes then applies the least-privilege default of
   * `email:send` rather than granting nothing.
   */
  createApiKey: async (name: string, scopes: string[] = []) => {
    return await apiKeyClient.createApiKey({ name, scopes });
  },
  listApiKeys: async (pageSize?: number, pageToken?: string) => {
    return await apiKeyClient.listApiKeys({ pageSize, pageToken });
  },
  deleteApiKey: async (id: string) => {
    return await apiKeyClient.deleteApiKey({ id });
  },
};
