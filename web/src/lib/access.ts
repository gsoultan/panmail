import { UserRole } from '../api/panmail/v1/auth_pb';

/**
 * Which parts of the console a role may use. The server decides — this mirrors
 * the procedure table in internal/auth/middlewares/policy.go so the console
 * does not offer a page or a button that only answers 403.
 *
 * Two different kinds of authority live here. Administering a tenant (its API
 * keys and users) is a tenant role. Settings that apply to the whole
 * deployment — the base URL, retention, redaction and the SMTP listener — are
 * one row for every tenant, and only a super admin may change them.
 */
export interface Access {
  /** API keys and users of the tenant being acted in. */
  administerTenant: boolean;
  /** System settings and the SMTP submission listener. */
  configureDeployment: boolean;
  /** Creating, editing and deleting tenants. */
  manageTenants: boolean;
}

export function accessFor(role: UserRole | undefined): Access {
  const superAdmin = role === UserRole.SUPER_ADMIN;
  return {
    administerTenant: superAdmin || role === UserRole.ADMIN,
    configureDeployment: superAdmin,
    manageTenants: superAdmin,
  };
}
