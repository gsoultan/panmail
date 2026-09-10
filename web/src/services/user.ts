import { userClient as client } from './client';
import { UserRole } from '../api/panmail/v1/auth_pb';

/**
 * Membership: one account may act in several tenants.
 *
 * The alternative was a second account per tenant, which the server refuses —
 * `users.email` is unique — so the same person needed an aliased address and a
 * separate password everywhere they worked.
 */
export const userService = {
  /** Tenants the given user may act in. Omit the id to ask about yourself. */
  listUserTenants: async (userId?: string) => {
    return await client.listUserTenants({ userId: userId ?? '' });
  },

  /**
   * Grant an existing account access to another tenant. An unspecified role
   * arrives as viewer: an account lent elsewhere should not carry its
   * authority across.
   */
  assignUserToTenant: async (userId: string, tenantId: string, role: UserRole) => {
    return await client.assignUserToTenant({ userId, tenantId, role });
  },

  /** Revoke a guest membership. The home tenant cannot be removed. */
  removeUserFromTenant: async (userId: string, tenantId: string) => {
    return await client.removeUserFromTenant({ userId, tenantId });
  },
};
