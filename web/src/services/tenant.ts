import { tenantClient as client } from './client';

/** A tenant's send ceiling. Zero on either field means unlimited. */
export interface SendLimits {
  sendRatePerMinute?: number;
  sendBurst?: number;
}

export const tenantService = {
  listTenants: async (pageSize?: number, pageToken?: string) => {
    return await client.listTenants({ pageSize, pageToken });
  },
  createTenant: async (name: string, retryPattern?: string[], limits?: SendLimits) => {
    return await client.createTenant({
      name,
      retryPattern,
      sendRatePerMinute: limits?.sendRatePerMinute ?? 0,
      sendBurst: limits?.sendBurst ?? 0,
    });
  },
  updateTenant: async (id: string, name: string, retryPattern?: string[], limits?: SendLimits) => {
    return await client.updateTenant({
      id,
      name,
      retryPattern,
      sendRatePerMinute: limits?.sendRatePerMinute ?? 0,
      sendBurst: limits?.sendBurst ?? 0,
    });
  },
  deleteTenant: async (id: string) => {
    return await client.deleteTenant({ id });
  },
};
