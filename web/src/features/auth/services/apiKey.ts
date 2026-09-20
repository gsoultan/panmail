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
  /**
   * Changes what an existing key may do, leaving the secret alone.
   *
   * `scopes` replaces the stored grant rather than adding to it, and an empty
   * list is not "no change" -- the server applies the same least-privilege
   * default it applies on create. Send the complete grant every time.
   */
  updateApiKey: async (id: string, name: string, scopes: string[]) => {
    return await apiKeyClient.updateApiKey({ id, name, scopes });
  },
  listApiKeys: async (pageSize?: number, pageToken?: string) => {
    return await apiKeyClient.listApiKeys({ pageSize, pageToken });
  },
  deleteApiKey: async (id: string) => {
    return await apiKeyClient.deleteApiKey({ id });
  },
};
