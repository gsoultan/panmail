import { beforeEach, describe, expect, mock, test } from 'bun:test';
import React, { useEffect } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClientProvider } from '@tanstack/react-query';
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router';

/**
 * The header switcher decides which tenant every request is answered as. What
 * it must not do is change the tenant and leave the previous one's screen up:
 * the rows cached for it, and the page state describing it, both have to go.
 */

mock.module('../services/tenant', () => ({
  tenantService: { listTenants: async () => ({ tenants: [] }) },
}));

const memberships = {
  tenants: [
    { tenantId: 't1', tenantName: 'Acme', role: 2, isHome: true, createdAt: '' },
    { tenantId: 't2', tenantName: 'Beta', role: 2, isHome: false, createdAt: '' },
  ],
};

mock.module('../services/user', () => ({
  userService: { listUserTenants: async () => memberships },
}));

const { AppLayout } = await import('./AppLayout');
const { useAuthStore } = await import('../store/authStore');
// The app's own client, so the subscription under test is the wired-up one
// rather than a copy of it.
const { queryClient } = await import('../services/queryClient');

const mountLog: string[] = [];

/** Stands in for whichever page is open when the tenant changes. */
const Probe = () => {
  useEffect(() => {
    mountLog.push('mount');
    return () => {
      mountLog.push('unmount');
    };
  }, []);
  return <div>the open page</div>;
};

const renderLayout = async () => {
  const rootRoute = createRootRoute();
  const layoutRoute = createRoute({
    getParentRoute: () => rootRoute,
    id: 'layout',
    component: AppLayout,
  });
  const pageRoute = createRoute({
    getParentRoute: () => layoutRoute,
    path: '/',
    component: Probe,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([layoutRoute.addChildren([pageRoute])]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  });

  await act(async () => {
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          {/* App.tsx registers its own router's type globally, so this local
              one needs a cast to be accepted as the prop. */}
          <RouterProvider router={router as never} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
};

/**
 * Open the switcher and choose a tenant by name.
 *
 * The dropdown animates in, so its options are not in the DOM on the tick after
 * the click that opens it. They are matched by text rather than by the option
 * role because jsdom has none of Mantine's stylesheet, which leaves the
 * portalled dropdown looking inaccessible to a by-role query.
 */
const pickTenant = async (name: string) => {
  await act(async () => {
    fireEvent.click(screen.getByPlaceholderText('Switch Tenant'));
    await new Promise((resolve) => setTimeout(resolve, 60));
  });
  await act(async () => {
    fireEvent.click(screen.getByText(name));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
};

beforeEach(() => {
  mountLog.length = 0;
  queryClient.clear();
  // An administrator of Acme, a guest of Beta. Seeded rather than awaited so the
  // switcher is on screen from the first render and each test is about the
  // switch itself.
  useAuthStore.getState().setAuth(
    { id: 'u1', email: 'ada@example.com', name: 'Ada', tenant_id: 't1', role: 2 },
    'token',
  );
  queryClient.setQueryData(['user-tenants', 'me'], memberships);
});

describe('the tenant switcher in the header', () => {
  test('acts as the tenant that was chosen', async () => {
    await renderLayout();

    await pickTenant('Beta');

    expect(useAuthStore.getState().selectedTenantID).toBe('t2');
  });

  test('forgets the rows the previous tenant answered with', async () => {
    await renderLayout();
    queryClient.setQueryData(['users'], { users: ['someone in Acme'] });

    await pickTenant('Beta');

    expect(queryClient.getQueryData(['users'])).toBeUndefined();
  });

  test('reloads the open page rather than reusing its state', async () => {
    await renderLayout();
    expect(mountLog).toEqual(['mount']);

    await pickTenant('Beta');

    // A page token is a cursor into the rows of the tenant it was issued for,
    // so the page has to start over rather than re-query with it.
    expect(mountLog).toEqual(['mount', 'unmount', 'mount']);
  });
});
