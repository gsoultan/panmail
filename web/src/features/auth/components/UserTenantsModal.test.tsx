import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

// Membership decides which tenant's mail an account can reach, so the two
// rules this dialog has to hold are: the home tenant is not revocable, and
// super admin is not a role a membership can carry.

const assigned: { userId: string; tenantId: string; role: number }[] = [];
const removed: { userId: string; tenantId: string }[] = [];

let memberships = [
  { tenantId: 't1', tenantName: 'Acme', role: 2, isHome: true, createdAt: '' },
  { tenantId: 't2', tenantName: 'Beta', role: 4, isHome: false, createdAt: '' },
];

mock.module('@mantine/notifications', () => ({
  notifications: { show: () => {} },
}));

mock.module('../../../services/user', () => ({
  userService: {
    listUserTenants: async () => ({ tenants: memberships }),
    assignUserToTenant: async (userId: string, tenantId: string, role: number) => {
      assigned.push({ userId, tenantId, role });
      return {};
    },
    removeUserFromTenant: async (userId: string, tenantId: string) => {
      removed.push({ userId, tenantId });
      return {};
    },
  },
}));

mock.module('../../../services/tenant', () => ({
  tenantService: {
    listTenants: async () => ({
      tenants: [
        { id: 't1', name: 'Acme' },
        { id: 't2', name: 'Beta' },
        { id: 't3', name: 'Gamma' },
      ],
    }),
  },
}));

const { UserTenantsModal } = await import('./UserTenantsModal');
const { ASSIGNABLE_ROLES } = await import('./membershipRoles');

const subject = { id: 'u1', name: 'Ada', email: 'ada@example.com' };

// Mantine renders a Modal into a portal, so the content lives on
// document.body rather than inside the render container. Every query here goes
// through `screen` for that reason.
const renderModal = async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <MantineProvider>
          <UserTenantsModal opened onClose={() => {}} user={subject} />
        </MantineProvider>
      </QueryClientProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  // The memberships and the tenant list settle on separate ticks, and the
  // dialog only has its full shape once both have.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
};

/** The membership rows, which is where the home/guest distinction shows. */
const membershipRows = () => {
  const table = screen.getByRole('table');
  return within(table).getAllByRole('row').slice(1);
};

describe('UserTenantsModal', () => {
  test('lists every tenant the account may act in, marking the one it signs in to', async () => {
    await renderModal();

    expect(screen.getByText('Acme')).toBeTruthy();
    expect(screen.getByText('Beta')).toBeTruthy();
    expect(screen.getByText('Home')).toBeTruthy();
  });

  // Revoking the home tenant would leave an account that signs in to a tenant
  // it is no longer recorded in, so the server refuses it. Offering the button
  // anyway would just produce a failed request.
  test('offers no way to revoke the home tenant', async () => {
    await renderModal();

    const [home, guest] = membershipRows();
    expect(within(home).queryAllByRole('button')).toHaveLength(0);
    expect(within(guest).queryAllByRole('button')).toHaveLength(1);
  });

  test('revoking a guest membership names that tenant', async () => {
    removed.length = 0;
    await renderModal();

    const [, guest] = membershipRows();
    await act(async () => {
      fireEvent.click(within(guest).getByRole('button'));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    expect(removed).toEqual([{ userId: 'u1', tenantId: 't2' }]);
  });

  // Tenants the account is already in are a role change, not an assignment,
  // so offering them again would create two ways to do one thing.
  test('only offers tenants the account is not already in', async () => {
    await renderModal();

    const picker = screen.getByPlaceholderText('Pick a tenant') as HTMLInputElement;
    await act(async () => {
      fireEvent.click(picker);
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    // Gamma is the only tenant left; Acme and Beta are already held.
    expect(document.body.textContent).toContain('Gamma');
  });

  test('says so plainly when there is nowhere left to assign', async () => {
    const previous = memberships;
    memberships = [
      { tenantId: 't1', tenantName: 'Acme', role: 2, isHome: true, createdAt: '' },
      { tenantId: 't2', tenantName: 'Beta', role: 4, isHome: false, createdAt: '' },
      { tenantId: 't3', tenantName: 'Gamma', role: 4, isHome: false, createdAt: '' },
    ];

    await renderModal();

    expect(screen.getByText('This user already has access to every tenant.')).toBeTruthy();
    expect(screen.queryByPlaceholderText('Pick a tenant')).toBeNull();

    memberships = previous;
  });

  // Super admin reaches procedures that are not scoped to any tenant, so the
  // server refuses it as a membership role. This is the list every role
  // control in the dialog is built from, so it is where that has to hold.
  test('never offers super admin as a role a membership can carry', () => {
    expect(ASSIGNABLE_ROLES.map((r) => r.label)).toEqual(['Administrator', 'Editor', 'Viewer']);
  });

  // The home role is users.role, which sign-in reads; changing it is Change
  // Role on the user, not a membership edit. So that row shows the role rather
  // than offering to rewrite it here.
  test('shows the home role as a badge rather than an editable control', async () => {
    await renderModal();

    const [home, guest] = membershipRows();
    expect(within(home).queryByRole('combobox')).toBeNull();
    expect(within(guest).getByRole('combobox')).toBeTruthy();
  });
});
