import { describe, expect, test } from 'bun:test';
import React, { useState } from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { ScopePicker } from './ScopePicker';
import { DEFAULT_SCOPE_IDS, type ScopeId } from '../scopes';

/**
 * The picker's job is to make an exclusion sayable and legible: "this key may
 * manage filter rules but may not release held mail" has to be both expressible
 * in two clicks and readable afterwards without counting checkboxes.
 */

/** Holds the selection, the way the page does, so the setter stays stable. */
const Harness: React.FC<{ initial?: ScopeId[]; onRender?: (v: Set<ScopeId>) => void }> = ({
  initial = [...DEFAULT_SCOPE_IDS],
  onRender,
}) => {
  const [value, setValue] = useState<Set<ScopeId>>(() => new Set(initial));
  onRender?.(value);
  return <ScopePicker value={value} onChange={setValue} />;
};

const mount = (props: React.ComponentProps<typeof Harness> = {}) =>
  render(
    <MantineProvider>
      <Harness {...props} />
    </MantineProvider>,
  );

/** The bordered block for a group, located by its heading. */
const groupBlock = (label: string): HTMLElement => {
  const heading = screen.getByText(label);
  const block = heading.closest('.mantine-Paper-root');
  if (!(block instanceof HTMLElement)) throw new Error(`no block for ${label}`);
  return block;
};

/**
 * Mantine renders the <label> as a sibling of the input rather than around it,
 * so walking up to the nearest ancestor that actually contains an input is what
 * ties a caption back to its control. Stopping at the first such ancestor keeps
 * a group heading from resolving to one of its children's checkboxes.
 */
const checkboxFor = (label: string, scope: HTMLElement): HTMLInputElement => {
  let el: HTMLElement | null = within(scope).getByText(label);
  while (el && !el.querySelector('input')) el = el.parentElement;
  const node = el?.querySelector('input');
  if (!(node instanceof HTMLInputElement)) throw new Error(`no checkbox for ${label}`);
  return node;
};

describe('ScopePicker', () => {
  test('opens on the least-privilege default', () => {
    mount();
    expect(checkboxFor('Send', groupBlock('Sending')).checked).toBe(true);
    expect(checkboxFor('Read', groupBlock('Providers')).checked).toBe(false);
  });

  test('every group in the catalogue is offered', () => {
    mount();
    for (const label of ['Sending', 'Providers', 'Templates', 'Suppressions', 'Webhooks', 'Events', 'Inbound', 'Filters']) {
      expect(screen.getByText(label)).toBeDefined();
    }
  });

  test('ticking a scope includes it', () => {
    let latest = new Set<ScopeId>();
    mount({ onRender: (v) => { latest = v; } });

    fireEvent.click(checkboxFor('Write', groupBlock('Templates')));
    expect(latest.has('templates:write')).toBe(true);
  });

  test('"Include all" grants the whole group', () => {
    let latest = new Set<ScopeId>();
    mount({ onRender: (v) => { latest = v; } });

    fireEvent.click(within(groupBlock('Filters')).getByText('Include all'));
    expect(latest.has('filters:read')).toBe(true);
    expect(latest.has('filters:write')).toBe(true);
    expect(latest.has('filters:release')).toBe(true);
  });

  test('"Exclude all" clears the group without touching the others', () => {
    let latest = new Set<ScopeId>();
    mount({ initial: ['email:send', 'filters:read', 'filters:write'], onRender: (v) => { latest = v; } });

    fireEvent.click(within(groupBlock('Filters')).getByText('Exclude all'));
    expect(latest.has('filters:read')).toBe(false);
    expect(latest.has('filters:write')).toBe(false);
    // The point of scoping the action to one group.
    expect(latest.has('email:send')).toBe(true);
  });

  test('excluding one scope from a granted group reads as an exception', () => {
    let latest = new Set<ScopeId>();
    mount({ onRender: (v) => { latest = v; } });

    const filters = groupBlock('Filters');
    fireEvent.click(within(filters).getByText('Include all'));
    // The case this exists for: rules yes, releasing held mail no.
    fireEvent.click(checkboxFor('Release', groupBlock('Filters')));

    expect(latest.has('filters:write')).toBe(true);
    expect(latest.has('filters:release')).toBe(false);
    expect(screen.getAllByText(/Filters: all except release/).length).toBeGreaterThan(0);
  });

  test('a partly-granted group shows an indeterminate parent', () => {
    mount({ initial: ['filters:read'] });
    const parent = checkboxFor('Filters', groupBlock('Filters'));
    expect(parent.indeterminate).toBe(true);
    expect(parent.checked).toBe(false);
  });

  test('a fully-granted group shows a checked parent', () => {
    mount({ initial: ['filters:read', 'filters:write', 'filters:release'] });
    const parent = checkboxFor('Filters', groupBlock('Filters'));
    expect(parent.checked).toBe(true);
    expect(parent.indeterminate).toBe(false);
  });

  test('the parent checkbox grants and then clears its group', () => {
    let latest = new Set<ScopeId>();
    mount({ initial: [], onRender: (v) => { latest = v; } });

    fireEvent.click(checkboxFor('Providers', groupBlock('Providers')));
    expect(latest.has('providers:read')).toBe(true);
    expect(latest.has('providers:write')).toBe(true);

    fireEvent.click(checkboxFor('Providers', groupBlock('Providers')));
    expect(latest.has('providers:read')).toBe(false);
  });

  test('a preset replaces the selection outright', () => {
    let latest = new Set<ScopeId>();
    mount({ initial: ['webhooks:write'], onRender: (v) => { latest = v; } });

    fireEvent.click(screen.getByText('Support desk'));

    expect(latest.has('inbound:read')).toBe(true);
    expect(latest.has('filters:release')).toBe(true);
    // Replaced, not merged -- otherwise a preset could only ever widen a grant.
    expect(latest.has('webhooks:write')).toBe(false);
    expect(latest.has('email:send')).toBe(false);
  });

  test('clearing everything falls back to the server default, and says so', () => {
    let latest = new Set<ScopeId>();
    mount({ onRender: (v) => { latest = v; } });

    fireEvent.click(screen.getByText('Clear'));
    expect(latest.size).toBe(0);
    expect(screen.getByText(/least-privilege default/)).toBeDefined();
  });

  test('the summary counts what is granted', () => {
    mount({ initial: ['email:send', 'templates:read'] });
    expect(screen.getByText(/\(2 scopes\)/)).toBeDefined();
  });

  test('a scope that changes state is flagged', () => {
    mount();
    // Send, writes and release are all marked; read-only scopes are not.
    expect(screen.getAllByText('changes state').length).toBeGreaterThan(0);
  });
});
