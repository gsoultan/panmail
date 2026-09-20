import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import {
  ALL_SCOPE_IDS,
  DEFAULT_SCOPE_IDS,
  SCOPES,
  SCOPE_GROUPS,
  SCOPE_PRESETS,
  describeGroup,
  excludedFrom,
  groupState,
  matchingPreset,
  resolveScopes,
  sanitize,
  summarize,
  type ScopeGroup,
} from './scopes';

const group = (resource: string): ScopeGroup => {
  const found = SCOPE_GROUPS.find((g) => g.resource === resource);
  if (!found) throw new Error(`no such group: ${resource}`);
  return found;
};

/**
 * The server is the authority and it fails quietly: NormalizeScopes drops
 * anything it does not recognise, so a catalogue listing a scope the server
 * removed mints a key silently missing it, and one missing a scope the server
 * added hides a capability nobody can grant. Neither shows up as an error, so
 * it is checked here rather than discovered in production.
 */
describe('catalogue matches the server', () => {
  const source = readFileSync('../internal/auth/entities/scope.go', 'utf8');

  // The const block gives name -> value; AllScopes gives which of those names a
  // caller may actually be granted. Reading both means a const that exists but
  // is deliberately not grantable does not count as drift.
  const values = new Map<string, string>();
  for (const [, name, value] of source.matchAll(/(Scope\w+)\s+Scope\s*=\s*"([^"]+)"/g)) {
    values.set(name, value);
  }

  const block = source.match(/var AllScopes = \[\]Scope\{([^}]*)\}/);
  const grantable = (block?.[1] ?? '')
    .split(',')
    .map((line) => line.replace(/\/\/.*$/gm, '').trim())
    .filter(Boolean)
    .map((name) => {
      const value = values.get(name);
      if (!value) throw new Error(`AllScopes names ${name}, which has no const`);
      return value;
    });

  test('the parse found something, so a rename cannot vacuously pass', () => {
    expect(values.size).toBeGreaterThan(0);
    expect(grantable.length).toBeGreaterThan(0);
  });

  test('every grantable server scope is in the catalogue', () => {
    expect([...grantable].sort()).toEqual([...ALL_SCOPE_IDS].sort());
  });

  test('the default matches DefaultScopes in scope.go', () => {
    const defaults = source.match(/var DefaultScopes = \[\]Scope\{([^}]*)\}/);
    const names = (defaults?.[1] ?? '').split(',').map((s) => s.trim()).filter(Boolean);
    expect(names.map((name) => values.get(name))).toEqual([...DEFAULT_SCOPE_IDS]);
  });
});

describe('the tree derives from the ids', () => {
  test('groups every scope exactly once', () => {
    const flattened = SCOPE_GROUPS.flatMap((g) => g.scopes.map((s) => s.id));
    expect(flattened.sort()).toEqual(SCOPES.map((s) => s.id).sort());
  });

  test('a group holds only its own resource', () => {
    for (const g of SCOPE_GROUPS) {
      for (const scope of g.scopes) {
        expect(scope.id.startsWith(`${g.resource}:`)).toBe(true);
      }
    }
  });

  test('splits the id into resource and action', () => {
    expect(group('filters').scopes.map((s) => s.action)).toEqual([
      'read',
      'write',
      'release',
    ]);
  });
});

describe('group state', () => {
  const filters = group('filters');

  test('reports none, partial and all', () => {
    expect(groupState(filters, new Set())).toBe('none');
    expect(groupState(filters, new Set(['filters:read']))).toBe('partial');
    expect(
      groupState(filters, new Set(['filters:read', 'filters:write', 'filters:release'])),
    ).toBe('all');
  });

  test('a scope from another group does not count toward it', () => {
    expect(groupState(filters, new Set(['email:send']))).toBe('none');
  });
});

describe('exclusions read as exclusions', () => {
  const filters = group('filters');

  test('names what was left out', () => {
    const selected = new Set(['filters:read', 'filters:write']);
    expect(excludedFrom(filters, selected).map((s) => s.id)).toEqual(['filters:release']);
    // The case the picker exists for: grant the group, withhold the dangerous one.
    expect(describeGroup(filters, selected)).toBe('Filters: all except release');
  });

  test('names the included half when that is the shorter list', () => {
    expect(describeGroup(filters, new Set(['filters:read']))).toBe('Filters: read');
  });

  test('a whole group says so', () => {
    expect(
      describeGroup(filters, new Set(['filters:read', 'filters:write', 'filters:release'])),
    ).toBe('Filters: all');
  });

  test('an empty group says nothing', () => {
    expect(describeGroup(filters, new Set())).toBe('');
  });
});

describe('resolving to what the server stores', () => {
  test('emits catalogue order, not selection order', () => {
    const selected = new Set(['filters:release', 'email:send', 'providers:read']);
    expect(resolveScopes(selected)).toEqual([
      'email:send',
      'providers:read',
      'filters:release',
    ]);
  });

  test('exclusion is resolved away, not sent', () => {
    // "All of filters except release" leaves the wire carrying two concrete
    // scopes -- there is no deny rule for the server to evaluate.
    const selected = new Set(['filters:read', 'filters:write']);
    expect(resolveScopes(selected)).toEqual(['filters:read', 'filters:write']);
  });

  test('drops an id the catalogue no longer knows', () => {
    expect(resolveScopes(new Set(['email:send', 'retired:scope']))).toEqual(['email:send']);
    expect([...sanitize(['email:send', 'retired:scope'])]).toEqual(['email:send']);
  });

  test('an empty selection sends nothing, leaving the server to apply its default', () => {
    expect(resolveScopes(new Set())).toEqual([]);
  });
});

describe('presets', () => {
  test('every preset grants only real scopes', () => {
    for (const preset of SCOPE_PRESETS) {
      for (const id of preset.scopes) {
        expect(ALL_SCOPE_IDS.has(id)).toBe(true);
      }
    }
  });

  test('no preset is empty or duplicated', () => {
    const ids = SCOPE_PRESETS.map((p) => p.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const preset of SCOPE_PRESETS) {
      expect(preset.scopes.length).toBeGreaterThan(0);
      expect(new Set(preset.scopes).size).toBe(preset.scopes.length);
    }
  });

  test('the audit preset can read everything and change nothing', () => {
    const audit = SCOPE_PRESETS.find((p) => p.id === 'audit');
    expect(audit).toBeDefined();
    expect(audit?.scopes.every((id) => id.endsWith(':read'))).toBe(true);
    expect([...audit!.scopes].sort()).toEqual(
      SCOPES.filter((s) => s.action === 'read').map((s) => s.id).sort(),
    );
  });

  test('the support preset can release held mail but not send new mail', () => {
    const support = SCOPE_PRESETS.find((p) => p.id === 'support-inbox');
    expect(support?.scopes).toContain('filters:release');
    expect(support?.scopes).not.toContain('email:send');
  });

  test('recognises a selection that matches a preset exactly', () => {
    const marketing = SCOPE_PRESETS.find((p) => p.id === 'marketing')!;
    expect(matchingPreset(new Set(marketing.scopes))?.id).toBe('marketing');
  });

  test('a preset with one scope removed no longer matches it', () => {
    const marketing = SCOPE_PRESETS.find((p) => p.id === 'marketing')!;
    const trimmed = new Set(marketing.scopes);
    trimmed.delete('suppressions:write');
    expect(matchingPreset(trimmed)).toBeUndefined();
  });
});

describe('summarising a granted key', () => {
  test('reads one phrase per group', () => {
    expect(summarize(['email:send', 'filters:read', 'filters:write'])).toEqual([
      'Sending: all',
      'Filters: all except release',
    ]);
  });

  test('ignores an id this build does not know', () => {
    expect(summarize(['email:send', 'retired:scope'])).toEqual(['Sending: all']);
  });

  test('an empty grant summarises to nothing', () => {
    expect(summarize([])).toEqual([]);
  });
});
