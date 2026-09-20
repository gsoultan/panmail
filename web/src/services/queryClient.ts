import { QueryClient, type QueryKey } from '@tanstack/react-query';
import { useAuthStore } from '../store/authStore';

/**
 * Query keys whose answers do not belong to one tenant.
 *
 * The tenant list and an account's memberships answer "where may I work?"
 * rather than "what is inside this tenant", and the header switcher is rendered
 * from them — dropping them mid-switch would empty the dropdown being used.
 *
 * Everything else counts as tenant data. That default is deliberate: dropping
 * too much costs one refetch, dropping too little shows one tenant's rows to
 * another.
 */
const TENANT_INDEPENDENT_KEYS: ReadonlySet<unknown> = new Set(['tenants', 'user-tenants']);

export const isTenantScopedKey = (key: QueryKey): boolean => !TENANT_INDEPENDENT_KEYS.has(key[0]);

export const queryClient = new QueryClient();

/**
 * Forget every answer the gateway gave while acting as the tenant being left.
 *
 * Removal, not invalidation: an invalidated query keeps serving its cached rows
 * while it refetches, which is exactly the cross-tenant flash this exists to
 * prevent. Removing also cancels requests already in flight, so a reply minted
 * for the old tenant cannot land in the new tenant's cache.
 */
export const forgetTenantScopedQueries = (client: QueryClient = queryClient): void => {
  client.removeQueries({ predicate: (query) => isTenantScopedKey(query.queryKey) });
};

/**
 * An identity here is (account, acting tenant). When it changes, the answers
 * cached under the old one are void.
 *
 * The two changes are not the same size. Switching tenant keeps the account, so
 * the lists naming where that account may work survive it. A different account
 * — or none, after signing out — keeps nothing: which tenants the last person
 * could reach is theirs, and the switcher must not offer their tenants to the
 * next person to sign in on this tab.
 *
 * This listens to the store instead of running inside a React effect on
 * purpose. Zustand notifies synchronously from within `set`, so the cache is
 * already empty before React renders anything under the new identity. An effect
 * runs after that render — late enough to paint the previous tenant's rows
 * first, which is the bug rather than the fix.
 *
 * Returns the unsubscribe function.
 */
export const watchIdentity = (client: QueryClient = queryClient): (() => void) =>
  useAuthStore.subscribe((state, previous) => {
    if (state.user?.id !== previous.user?.id) {
      client.removeQueries();
      return;
    }
    if (state.selectedTenantID !== previous.selectedTenantID) {
      forgetTenantScopedQueries(client);
    }
  });

// Wired where the client is created so no call site has to remember to do it.
watchIdentity();
