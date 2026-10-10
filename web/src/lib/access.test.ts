import { describe, expect, test } from 'bun:test';
import { UserRole } from '../api/panmail/v1/auth_pb';
import { accessFor } from './access';

describe('accessFor', () => {
  test('a super admin may do everything', () => {
    expect(accessFor(UserRole.SUPER_ADMIN)).toEqual({
      administerTenant: true,
      configureDeployment: true,
      manageTenants: true,
    });
  });

  // System settings are deployment-wide. The server refuses an administrator
  // since they became super-admin only, so the console must not offer them.
  test('an administrator runs their tenant but not the deployment', () => {
    expect(accessFor(UserRole.ADMIN)).toEqual({
      administerTenant: true,
      configureDeployment: false,
      manageTenants: false,
    });
  });

  test.each([UserRole.EDITOR, UserRole.VIEWER, UserRole.UNSPECIFIED, undefined])(
    'role %p administers nothing',
    (role) => {
      expect(accessFor(role)).toEqual({
        administerTenant: false,
        configureDeployment: false,
        manageTenants: false,
      });
    },
  );
});
