import React, { useCallback, useMemo } from 'react';
import { Badge, Box, Button, Checkbox, Group, Paper, Stack, Text, rem } from '@mantine/core';
import {
  SCOPE_GROUPS,
  SCOPE_PRESETS,
  describeGroup,
  groupState,
  matchingPreset,
  type GroupState,
  type ScopeDef,
  type ScopeGroup,
  type ScopeId,
} from '../scopes';

/**
 * Picks the capabilities an API key is granted.
 *
 * `onChange` is deliberately a state setter rather than a value callback. Every
 * handler below is then a useCallback over nothing but the setter, which
 * useState guarantees is stable, so the memoised rows keep their identity
 * across a toggle and only the row whose `checked` actually moved re-renders.
 * Taking `(next: Set) => void` instead would rebuild every handler on each
 * keystroke and defeat the memo.
 */
export interface ScopePickerProps {
  value: ReadonlySet<ScopeId>;
  onChange: React.Dispatch<React.SetStateAction<Set<ScopeId>>>;
}

const ScopeRow = React.memo(function ScopeRow({
  scope,
  checked,
  onToggle,
}: {
  scope: ScopeDef;
  checked: boolean;
  onToggle: (id: ScopeId) => void;
}) {
  // The id rides on the DOM node rather than in a closure, so this handler does
  // not have to be rebuilt per row on every render.
  const handle = useCallback(
    (event: React.ChangeEvent<HTMLInputElement>) => {
      onToggle(event.currentTarget.dataset.scope as ScopeId);
    },
    [onToggle],
  );

  return (
    <Checkbox
      checked={checked}
      onChange={handle}
      data-scope={scope.id}
      size="sm"
      label={
        <Group gap={6} wrap="nowrap">
          <Text size="sm" fw={600}>
            {scope.label}
          </Text>
          <Text size="xs" c="dimmed" ff="monospace">
            {scope.id}
          </Text>
          {scope.sensitive && (
            <Badge size="xs" variant="light" color="orange" radius="sm">
              changes state
            </Badge>
          )}
        </Group>
      }
      description={scope.description}
    />
  );
});

const GroupBlock = React.memo(function GroupBlock({
  group,
  state,
  summary,
  selected,
  onToggleScope,
  onSetGroup,
}: {
  group: ScopeGroup;
  state: GroupState;
  summary: string;
  selected: ReadonlySet<ScopeId>;
  onToggleScope: (id: ScopeId) => void;
  onSetGroup: (resource: string, include: boolean) => void;
}) {
  const includeAll = useCallback(
    () => onSetGroup(group.resource, true),
    [onSetGroup, group.resource],
  );
  const excludeAll = useCallback(
    () => onSetGroup(group.resource, false),
    [onSetGroup, group.resource],
  );

  return (
    <Paper
      withBorder
      radius="md"
      p="md"
      style={{
        backgroundColor:
          state === 'none'
            ? 'transparent'
            : 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))',
      }}
    >
      <Group justify="space-between" align="flex-start" wrap="nowrap" mb="sm">
        <Box style={{ minWidth: 0 }}>
          <Group gap={8}>
            <Checkbox
              checked={state === 'all'}
              indeterminate={state === 'partial'}
              onChange={() => onSetGroup(group.resource, state !== 'all')}
              size="sm"
              label={
                <Text size="sm" fw={800}>
                  {group.label}
                </Text>
              }
            />
            {state === 'partial' && (
              <Badge size="xs" variant="light" color="blue" radius="sm">
                partial
              </Badge>
            )}
          </Group>
          <Text size="xs" c="dimmed" mt={4}>
            {summary || group.description}
          </Text>
        </Box>

        <Group gap={4} wrap="nowrap">
          <Button
            size="compact-xs"
            variant={state === 'all' ? 'light' : 'subtle'}
            onClick={includeAll}
            disabled={state === 'all'}
          >
            Include all
          </Button>
          <Button
            size="compact-xs"
            variant="subtle"
            color="gray"
            onClick={excludeAll}
            disabled={state === 'none'}
          >
            Exclude all
          </Button>
        </Group>
      </Group>

      <Stack gap="xs" pl={rem(28)}>
        {group.scopes.map((scope) => (
          <ScopeRow
            key={scope.id}
            scope={scope}
            checked={selected.has(scope.id)}
            onToggle={onToggleScope}
          />
        ))}
      </Stack>
    </Paper>
  );
});

export const ScopePicker: React.FC<ScopePickerProps> = ({ value, onChange }) => {
  const toggleScope = useCallback(
    (id: ScopeId) => {
      onChange((prev) => {
        const next = new Set(prev);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        return next;
      });
    },
    [onChange],
  );

  const setGroup = useCallback(
    (resource: string, include: boolean) => {
      onChange((prev) => {
        const group = SCOPE_GROUPS.find((g) => g.resource === resource);
        if (!group) return prev;
        const next = new Set(prev);
        for (const scope of group.scopes) {
          if (include) next.add(scope.id);
          else next.delete(scope.id);
        }
        return next;
      });
    },
    [onChange],
  );

  const applyPreset = useCallback(
    (event: React.MouseEvent<HTMLButtonElement>) => {
      const id = event.currentTarget.dataset.preset;
      const preset = SCOPE_PRESETS.find((p) => p.id === id);
      if (preset) onChange(new Set(preset.scopes));
    },
    [onChange],
  );

  const clearAll = useCallback(() => onChange(new Set()), [onChange]);

  // One pass over the catalogue per change rather than one per group per
  // render, and the rows below read their state straight out of it.
  const view = useMemo(
    () =>
      SCOPE_GROUPS.map((group) => ({
        group,
        state: groupState(group, value),
        summary: describeGroup(group, value),
      })),
    [value],
  );

  const active = useMemo(() => matchingPreset(value), [value]);
  const granted = useMemo(
    () => view.filter((v) => v.summary).map((v) => v.summary),
    [view],
  );

  return (
    <Stack gap="sm">
      <Box>
        <Text size="xs" fw={700} tt="uppercase" c="dimmed" mb={6}>
          Start from an integration
        </Text>
        <Group gap={6}>
          {SCOPE_PRESETS.map((preset) => (
            <Button
              key={preset.id}
              data-preset={preset.id}
              onClick={applyPreset}
              size="compact-sm"
              radius="xl"
              variant={active?.id === preset.id ? 'filled' : 'default'}
              title={preset.description}
            >
              {preset.label}
            </Button>
          ))}
          <Button
            size="compact-sm"
            radius="xl"
            variant="subtle"
            color="gray"
            onClick={clearAll}
            disabled={value.size === 0}
          >
            Clear
          </Button>
        </Group>
        {active && (
          <Text size="xs" c="dimmed" mt={6}>
            {active.description}
          </Text>
        )}
      </Box>

      <Stack gap="xs">
        {view.map(({ group, state, summary }) => (
          <GroupBlock
            key={group.resource}
            group={group}
            state={state}
            summary={summary}
            selected={value}
            onToggleScope={toggleScope}
            onSetGroup={setGroup}
          />
        ))}
      </Stack>

      <Paper
        withBorder
        radius="md"
        p="sm"
        style={{
          backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))',
        }}
      >
        <Text size="xs" fw={700} tt="uppercase" c="dimmed" mb={4}>
          This key will be able to
        </Text>
        {granted.length === 0 ? (
          <Text size="sm" c="dimmed">
            Nothing selected — the key will be created with the least-privilege
            default, <Text span ff="monospace" size="sm">email:send</Text>.
          </Text>
        ) : (
          <Text size="sm" fw={500}>
            {granted.join(' · ')}{' '}
            <Text span c="dimmed" size="sm">
              ({value.size} {value.size === 1 ? 'scope' : 'scopes'})
            </Text>
          </Text>
        )}
      </Paper>
    </Stack>
  );
};
