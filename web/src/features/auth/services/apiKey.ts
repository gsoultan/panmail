import { apiKeyClient } from "../../../services/client";
import { CreateApiKeyRequest, ListApiKeysRequest, DeleteApiKeyRequest } from "../../../api/panmail/v1/auth_pb";

export const apiKeyService = {
  createApiKey: async (name: string) => {
    return await apiKeyClient.createApiKey({ name });
  },
  listApiKeys: async (pageSize?: number, pageToken?: string) => {
    return await apiKeyClient.listApiKeys({ pageSize, pageToken });
  },
  deleteApiKey: async (id: string) => {
    return await apiKeyClient.deleteApiKey({ id });
  },
};
