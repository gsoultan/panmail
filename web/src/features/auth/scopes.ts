/**
 * The capability catalogue an API key is granted from.
 *
 * Scopes are `resource:action`, which is the nesting: the picker groups by
 * resource and the actions hang beneath it. The tree is derived from the ids
 * rather than written out a second time, so adding a scope to SCOPES is the
 * whole change — no group definition to keep in step.
 *
 * `internal/auth/entities/scope.go` is the authority. The server drops anything
 * it does not recognise (NormalizeScopes), so a catalogue that drifts ahead of
 * it fails silently: the key is minted without the scope the operator ticked.
 * scopes.test.ts reads scope.go and fails if the two disagree.
 */

export type ScopeId = string;

export interface ScopeDef {
  id: ScopeId;
  /** The action half of the id, e.g. `read`. */
  action: string;
  label: string;
  /** What holding this permits, in terms of what the key can do to the tenant. */
  description: string;
  /** Marks a scope that changes state or releases mail, for the risk cue. */
  sensitive?: boolean;
}

export interface ScopeGroup {
  /** The resource half of the id, e.g. `providers`. */
  resource: string;
  label: string;
  description: string;
  scopes: ScopeDef[];
}

const def = (
  id: ScopeId,
  label: string,
  description: string,
  sensitive = false,
): ScopeDef => ({
  id,
  action: id.slice(id.indexOf(':') + 1),
  label,
  description,
  ...(sensitive ? { sensitive: true } : {}),
});

/** Every scope the server recognises, in the order the picker shows them. */
export const SCOPES: readonly ScopeDef[] = Object.freeze([
  def('email:send', 'Send', 'Send mail through this tenant’s providers.', true),

  def('providers:read', 'Read', 'List providers and see how they are configured.'),
  def('providers:write', 'Write', 'Add, edit and remove sending providers.', true),

  def('templates:read', 'Read', 'List templates and read their contents.'),
  def('templates:write', 'Write', 'Create, edit and delete templates.', true),

  def('suppressions:read', 'Read', 'See which addresses are suppressed, and why.'),
  def(
    'suppressions:write',
    'Write',
    'Suppress an address, or lift a suppression so mail reaches it again.',
    true,
  ),

  def('webhooks:read', 'Read', 'List webhook endpoints and their delivery history.'),
  def('webhooks:write', 'Write', 'Add, edit and remove webhook endpoints.', true),

  def('events:read', 'Read', 'Read delivery events: sends, bounces, opens and clicks.'),

  def('inbound:read', 'Read', 'Read mail delivered to this tenant’s inbound addresses.'),

  def('filters:read', 'Read', 'List filtering rules and the messages they have held.'),
  def('filters:write', 'Write', 'Create, edit and delete filtering rules.', true),
  def(
    'filters:release',
    'Release',
    'Release a held message, putting it on the wire now. Separate from write on purpose.',
    true,
  ),
]);

const GROUP_META: Record<string, { label: string; description: string }> = {
  email: { label: 'Sending', description: 'Putting mail on the wire.' },
  providers: { label: 'Providers', description: 'The accounts mail is sent through.' },
  templates: { label: 'Templates', description: 'Reusable message bodies.' },
  suppressions: {
    label: 'Suppressions',
    description: 'Addresses this tenant must not mail.',
  },
  webhooks: { label: 'Webhooks', description: 'Where delivery events are posted.' },
  events: { label: 'Events', description: 'The delivery record: sends, bounces, opens.' },
  inbound: { label: 'Inbound', description: 'Mail arriving at this tenant.' },
  filters: { label: 'Filters', description: 'Rules that hold or drop mail, and the queue they fill.' },
};

/**
 * The catalogue as a tree, built once at module load.
 *
 * The picker renders straight off this and never rebuilds it, so grouping cost
 * is paid once per page load rather than once per keystroke.
 */
export const SCOPE_GROUPS: readonly ScopeGroup[] = Object.freeze(
  (() => {
    const order: string[] = [];
    const byResource = new Map<string, ScopeDef[]>();

    for (const scope of SCOPES) {
      const resource = scope.id.slice(0, scope.id.indexOf(':'));
      let bucket = byResource.get(resource);
      if (!bucket) {
        bucket = [];
        byResource.set(resource, bucket);
        order.push(resource);
      }
      bucket.push(scope);
    }

    return order.map((resource) => ({
      resource,
      label: GROUP_META[resource]?.label ?? resource,
      description: GROUP_META[resource]?.description ?? '',
      scopes: byResource.get(resource) ?? [],
    }));
  })(),
);

/** Every valid id, for O(1) membership tests. */
export const ALL_SCOPE_IDS: ReadonlySet<ScopeId> = new Set(SCOPES.map((s) => s.id));

/**
 * What a key gets when nothing is chosen, mirroring `DefaultScopes` in
 * scope.go. Sending mail is the reason API keys exist; everything else is
 * asked for.
 */
export const DEFAULT_SCOPE_IDS: readonly ScopeId[] = Object.freeze(['email:send']);

/** How much of a group is granted. `partial` is the include-with-exceptions case. */
export type GroupState = 'none' | 'partial' | 'all';

export function groupState(group: ScopeGroup, selected: ReadonlySet<ScopeId>): GroupState {
  let held = 0;
  for (const scope of group.scopes) {
    if (selected.has(scope.id)) held++;
  }
  if (held === 0) return 'none';
  return held === group.scopes.length ? 'all' : 'partial';
}

/**
 * The excluded members of a partially-granted group.
 *
 * This is what makes an exclusion legible after the fact: a key holding
 * filters read and write reads as "all except release" rather than as two
 * unrelated ticks.
 */
export function excludedFrom(group: ScopeGroup, selected: ReadonlySet<ScopeId>): ScopeDef[] {
  return group.scopes.filter((scope) => !selected.has(scope.id));
}

/** A one-line description of a group's grant, for the summary row. */
export function describeGroup(group: ScopeGroup, selected: ReadonlySet<ScopeId>): string {
  const state = groupState(group, selected);
  if (state === 'none') return '';
  if (state === 'all') return `${group.label}: all`;

  const excluded = excludedFrom(group, selected);
  const included = group.scopes.filter((scope) => selected.has(scope.id));

  // Naming the shorter half keeps the summary readable either way: one
  // exception out of three reads as an exception, two out of three as a pick.
  return excluded.length < included.length
    ? `${group.label}: all except ${excluded.map((s) => s.label.toLowerCase()).join(', ')}`
    : `${group.label}: ${included.map((s) => s.label.toLowerCase()).join(', ')}`;
}

/**
 * Resolves a selection to the flat list the server stores.
 *
 * Exclusion is an authoring idea, not a stored one: what goes over the wire is
 * the concrete set that survived it. That keeps the server's check a single set
 * membership test on the request path, with no deny rules to evaluate and
 * nothing to migrate. The cost is that a scope added to a later release is not
 * retroactively granted to an existing key, which is the safer direction.
 *
 * Iterating SCOPES rather than the selection gives a stable catalogue order and
 * drops anything stale in one pass.
 */
export function resolveScopes(selected: ReadonlySet<ScopeId>): ScopeId[] {
  const out: ScopeId[] = [];
  for (const scope of SCOPES) {
    if (selected.has(scope.id)) out.push(scope.id);
  }
  return out;
}

/** Drops ids the catalogue no longer knows, so a stale preset cannot poison a grant. */
export function sanitize(ids: Iterable<ScopeId>): Set<ScopeId> {
  const out = new Set<ScopeId>();
  for (const id of ids) {
    if (ALL_SCOPE_IDS.has(id)) out.add(id);
  }
  return out;
}

export interface ScopePreset {
  id: string;
  label: string;
  /** The integration this exists for, in business terms. */
  description: string;
  scopes: readonly ScopeId[];
}

/**
 * Starting points named for the integrations people actually build.
 *
 * A preset is a starting point, not a mode: picking one fills the tree in and
 * the operator carries on adjusting it, which is where exclusion earns its
 * keep — "marketing, but it must not be able to lift a suppression".
 */
export const SCOPE_PRESETS: readonly ScopePreset[] = Object.freeze([
  {
    id: 'transactional',
    label: 'Order & account email',
    description:
      'A storefront or SaaS backend sending receipts, password resets and shipping notices. Reads templates, respects the suppression list, changes neither.',
    scopes: ['email:send', 'templates:read', 'suppressions:read'],
  },
  {
    id: 'marketing',
    label: 'Marketing campaigns',
    description:
      'A campaign tool that writes its own templates, honours unsubscribes by adding to the suppression list, and reads back what was delivered.',
    scopes: [
      'email:send',
      'templates:read',
      'templates:write',
      'suppressions:read',
      'suppressions:write',
      'events:read',
    ],
  },
  {
    id: 'deliverability',
    label: 'Deliverability reporting',
    description:
      'A nightly job pulling bounce, open and click data into a warehouse or BI dashboard. Reads everything it needs and can send nothing.',
    scopes: ['events:read', 'suppressions:read', 'webhooks:read'],
  },
  {
    id: 'support-inbox',
    label: 'Support desk',
    description:
      'A helpdesk that reads inbound mail into tickets and lets an agent release a legitimate message the filters held.',
    scopes: ['inbound:read', 'filters:read', 'filters:release'],
  },
  {
    id: 'audit',
    label: 'Compliance audit',
    description:
      'A reviewer or archiver that must see everything and change nothing. Every read scope, no write and no send.',
    scopes: SCOPES.filter((s) => s.action === 'read').map((s) => s.id),
  },
  {
    id: 'provider-ops',
    label: 'Provider failover',
    description:
      'An ops automation that watches delivery health and reconfigures or swaps a provider when one degrades.',
    scopes: ['providers:read', 'providers:write', 'events:read'],
  },
]);

/** The preset exactly matching a selection, if any, so the UI can show it as active. */
export function matchingPreset(selected: ReadonlySet<ScopeId>): ScopePreset | undefined {
  return SCOPE_PRESETS.find(
    (preset) =>
      preset.scopes.length === selected.size &&
      preset.scopes.every((id) => selected.has(id)),
  );
}

/**
 * A granted key's capabilities, one phrase per group, for listing an existing
 * key. Stale ids are dropped first so a key minted against an older catalogue
 * still reads sensibly rather than showing a scope this build cannot explain.
 */
export function summarize(ids: Iterable<ScopeId>): string[] {
  const selected = sanitize(ids);
  const out: string[] = [];
  for (const group of SCOPE_GROUPS) {
    const phrase = describeGroup(group, selected);
    if (phrase) out.push(phrase);
  }
  return out;
}
