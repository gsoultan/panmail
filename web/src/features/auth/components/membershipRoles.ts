import { UserRole } from '../../../api/panmail/v1/auth_pb';

/**
 * The roles a tenant membership may carry, and the only list any role control
 * in the membership dialog is built from.
 *
 * Super admin is deliberately absent. It reaches procedures that are not
 * scoped to a tenant — ListTenants, CreateTenant — so the server refuses it as
 * a membership role rather than let a global administrator be minted one
 * tenant at a time. Promotion to super admin is a change to the user, not to
 * one of their memberships.
 */
export const ASSIGNABLE_ROLES = [
  { value: UserRole.ADMIN.toString(), label: 'Administrator' },
  { value: UserRole.EDITOR.toString(), label: 'Editor' },
  { value: UserRole.VIEWER.toString(), label: 'Viewer' },
];

/** How each role is shown once held. Super admin appears here but not above. */
export const MEMBERSHIP_ROLE_LABELS: Partial<Record<UserRole, { label: string; color: string }>> = {
  [UserRole.SUPER_ADMIN]: { label: 'Super Admin', color: 'red' },
  [UserRole.ADMIN]: { label: 'Administrator', color: 'blue' },
  [UserRole.EDITOR]: { label: 'Editor', color: 'green' },
  [UserRole.VIEWER]: { label: 'Viewer', color: 'gray' },
};
