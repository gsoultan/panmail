import { afterEach, describe, expect, test } from 'bun:test';
import { QueryClient } from '@tanstack/react-query';
import { useAuthStore } from '../store/authStore';
import { isTenantScopedKey, watchIdentity } from './queryClient';

/**
 * The header switcher changes which tenant the gateway answers as — every
 * request carries the choice in X-Tenant-ID. Nothing else about a request
 * changes, so TanStack Query, which keys its cache by query key alone, happily
 * serves the tenant we just left. These tests pin the cache to the identity
 * that produced it.
 */

const account = (id: string, tenantId: string) => ({
  id,
  email: `${id}@example.com`,
  name: id,
  tenant_id: tenantId,
  role: 2,
});

const signIn = (userId: string, tenantId: string) =>
  useAuthStore.getState().setAuth(account(userId, tenantId), 'token');

const switchTo = (tenantId: string) => useAuthStore.getState().setSelectedTenantID(tenantId);

let unwatch: (() => void) | undefined;

/** A client watched the same way the app's singleton is, but private to one test. */
const watchedClient = () => {
  const client = new QueryClient();
  unwatch = watchIdentity(client);
  return client;
};

afterEach(() => {
  unwatch?.();
  unwatch = undefined;
  useAuthStore.getState().clearAuth();
});

describe('switching the acting tenant', () => {
  test('forgets the answers cached for the tenant being left', () => {
    const client = watchedClient();
    signIn('u1', 't1');
    client.setQueryData(['users', undefined, '10'], { users: ['someone in t1'] });
    client.setQueryData(['emailProviders'], { providers: ['t1 smtp'] });
    client.setQueryData(['dashboardMetrics', 1, 2], { sent: 41 });

    switchTo('t2');

    expect(client.getQueryData(['users', undefined, '10'])).toBeUndefined();
    expect(client.getQueryData(['emailProviders'])).toBeUndefined();
    expect(client.getQueryData(['dashboardMetrics', 1, 2])).toBeUndefined();
  });

  test('keeps the lists the switcher itself is rendered from', () => {
    const client = watchedClient();
    signIn('u1', 't1');
    const tenants = { tenants: [{ id: 't1' }, { id: 't2' }] };
    const memberships = { tenants: [{ tenantId: 't1' }, { tenantId: 't2' }] };
    client.setQueryData(['tenants'], tenants);
    client.setQueryData(['user-tenants', 'me'], memberships);

    switchTo('t2');

    expect(client.getQueryData<typeof tenants>(['tenants'])).toEqual(tenants);
    expect(client.getQueryData<typeof memberships>(['user-tenants', 'me'])).toEqual(memberships);
  });

  test('empties the cache before anything can render under the new tenant', () => {
    const client = watchedClient();
    signIn('u1', 't1');
    client.setQueryData(['users'], { users: ['someone in t1'] });

    // A component re-renders from a store subscription of its own. Whatever the
    // first subscriber can see, React can see — so the rows have to be gone by
    // then. Moving the purge into an effect, which runs after that render,
    // fails here.
    let visibleToTheNextRender: unknown = 'never read';
    const stopReading = useAuthStore.subscribe(() => {
      visibleToTheNextRender = client.getQueryData(['users']);
    });

    switchTo('t2');
    stopReading();

    expect(visibleToTheNextRender).toBeUndefined();
  });

  test('drops a reply already in flight for the old tenant', async () => {
    const client = watchedClient();
    signIn('u1', 't1');

    let deliver: (rows: { users: string[] }) => void = () => {};
    const reply = new Promise<{ users: string[] }>((resolve) => {
      deliver = resolve;
    });
    const inFlight = client
      .fetchQuery({ queryKey: ['users'], queryFn: () => reply })
      .catch(() => undefined);

    switchTo('t2');
    deliver({ users: ['someone in t1'] });
    await inFlight;

    expect(client.getQueryData(['users'])).toBeUndefined();
  });

  test('leaves the cache alone when the tenant chosen is the one already in use', () => {
    const client = watchedClient();
    signIn('u1', 't1');
    const rows = { users: ['someone in t1'] };
    client.setQueryData(['users'], rows);

    switchTo('t1');

    expect(client.getQueryData<typeof rows>(['users'])).toEqual(rows);
  });
});

describe('changing account', () => {
  test('a different person signing into the same tenant inherits nothing', () => {
    const client = watchedClient();
    signIn('u1', 't1');
    client.setQueryData(['users'], { users: ['visible to u1'] });

    signIn('u2', 't1');

    expect(client.getQueryData(['users'])).toBeUndefined();
  });

  // ListUserTenants names every tenant an account belongs to, which is why
  // asking about somebody else takes super admin. Leaving one account's answer
  // in the cache would hand the next account the same list, and the switcher is
  // built straight from it.
  test('does not offer the previous account\'s tenants in the switcher', () => {
    const client = watchedClient();
    signIn('u1', 't1');
    client.setQueryData(['user-tenants', 'me'], { tenants: [{ tenantId: 't9' }] });

    signIn('u2', 't1');

    expect(client.getQueryData(['user-tenants', 'me'])).toBeUndefined();
  });

  test('signing out leaves nothing behind for the next account', () => {
    const client = watchedClient();
    signIn('u1', 't1');
    client.setQueryData(['logs', undefined, '25'], { logs: ['delivery to a t1 address'] });
    client.setQueryData(['user-tenants', 'me'], { tenants: [{ tenantId: 't1' }] });

    useAuthStore.getState().clearAuth();

    expect(client.getQueryData(['logs', undefined, '25'])).toBeUndefined();
    expect(client.getQueryData(['user-tenants', 'me'])).toBeUndefined();
  });
});

describe('isTenantScopedKey', () => {
  const cases: { name: string; key: unknown[]; scoped: boolean }[] = [
    { name: 'the list of all tenants is not any one tenant', key: ['tenants'], scoped: false },
    { name: 'nor is a page of it', key: ['tenants', 'next', '10'], scoped: false },
    { name: 'nor the tenants an account belongs to', key: ['user-tenants', 'me'], scoped: false },
    { name: "a tenant's users are its own", key: ['users'], scoped: true },
    { name: "so are its logs", key: ['logs', undefined, '25'], scoped: true },
    { name: 'so are its metrics', key: ['dashboardMetrics', 1, 2], scoped: true },
    { name: 'and anything not named as shared', key: ['somethingNew'], scoped: true },
  ];

  for (const tc of cases) {
    test(tc.name, () => {
      expect(isTenantScopedKey(tc.key)).toBe(tc.scoped);
    });
  }
});
