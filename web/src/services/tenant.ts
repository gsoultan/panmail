import { tenantClient as client } from './client';

export const tenantService = {
  listTenants: async (pageSize?: number, pageToken?: string) => {
    return await client.listTenants({ pageSize, pageToken });
  },
  createTenant: async (name: string, retryPattern?: string[]) => {
    return await client.createTenant({ name, retryPattern });
  },
  updateTenant: async (id: string, name: string, retryPattern?: string[]) => {
    return await client.updateTenant({ id, name, retryPattern });
  },
  deleteTenant: async (id: string) => {
    return await client.deleteTenant({ id });
  },
};
