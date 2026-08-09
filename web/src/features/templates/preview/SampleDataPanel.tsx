import React, { useMemo, useState } from 'react';
import {
  Stack, Group, Text, Textarea, Button, Badge, Alert, Popover, ActionIcon, Tooltip, Code,
} from '@mantine/core';
import { IconDatabase, IconWand, IconAlertTriangle } from '@tabler/icons-react';
import { collectVariables, suggestSampleData, type PreviewData } from './renderPreview';

interface SampleDataPanelProps {
  /** The template being previewed, used to work out which variables it needs. */
  html: string;
  value: string;
  onChange: (json: string) => void;
}

/**
 * Lets the author preview a template with real values.
 *
 * Without this the preview substitutes `[name]` for every variable, which
 * answers none of the questions the preview exists to answer. Real values are
 * not the length of their placeholders, and length is what breaks an email:
 * a name that wraps to a second line, a product title that overflows a table
 * cell, an order number that pushes a button off the edge. Conditionals are the
 * other half — with placeholders every branch renders, so the empty state, the
 * non-VIP variant and the "no items" case are all invisible until someone
 * receives one.
 */
export const SampleDataPanel: React.FC<SampleDataPanelProps> = ({ html, value, onChange }) => {
  const [opened, setOpened] = useState(false);

  const variables = useMemo(() => collectVariables(html), [html]);

  const parsed = useMemo(() => {
    if (!value.trim()) return { ok: true as const, data: undefined };
    try {
      const data = JSON.parse(value);
      if (data === null || typeof data !== 'object' || Array.isArray(data)) {
        return { ok: false as const, error: 'Sample data has to be a JSON object.' };
      }
      return { ok: true as const, data: data as PreviewData };
    } catch (e) {
      return { ok: false as const, error: (e as Error).message };
    }
  }, [value]);

  // Which variables the current data does not cover, so the author can see what
  // is still rendering as a placeholder.
  const missing = useMemo(() => {
    if (!parsed.ok || !parsed.data) return variables;
    const data = parsed.data;
    return variables.filter((v) => {
      const root = v.split('.')[0];
      return !(root in data);
    });
  }, [variables, parsed]);

  const active = parsed.ok && parsed.data !== undefined;

  return (
    <Popover width={420} position="bottom-end" shadow="md" withArrow opened={opened} onChange={setOpened}>
      <Popover.Target>
        <Tooltip label={active ? 'Previewing with sample data' : 'Preview with sample data'}>
          <ActionIcon
            variant={active ? 'filled' : 'light'}
            color={parsed.ok ? 'brand' : 'red'}
            onClick={() => setOpened((o) => !o)}
            aria-label="Sample data for the preview"
          >
            <IconDatabase size={18} />
          </ActionIcon>
        </Tooltip>
      </Popover.Target>

      <Popover.Dropdown>
        <Stack gap="xs">
          <Group justify="space-between" wrap="nowrap">
            <div>
              <Text size="sm" fw={600}>Sample data</Text>
              <Text size="xs" c="dimmed">
                Renders the preview with real values instead of placeholders
              </Text>
            </div>
            <Button
              size="compact-xs"
              variant="light"
              leftSection={<IconWand size={12} />}
              onClick={() => onChange(JSON.stringify(suggestSampleData(html), null, 2))}
              disabled={variables.length === 0}
            >
              Fill in
            </Button>
          </Group>

          {variables.length > 0 && (
            <Group gap={4} wrap="wrap">
              <Text size="xs" c="dimmed">Used:</Text>
              {variables.map((v) => (
                <Badge
                  key={v}
                  size="xs"
                  variant="light"
                  color={missing.includes(v) ? 'gray' : 'teal'}
                >
                  {v}
                </Badge>
              ))}
            </Group>
          )}

          <Textarea
            placeholder={'{\n  "name": "Alexandra",\n  "items": [{ "name": "Widget" }]\n}'}
            value={value}
            onChange={(e) => onChange(e.currentTarget.value)}
            minRows={8}
            autosize
            maxRows={16}
            styles={{ input: { fontFamily: 'monospace', fontSize: 12 } }}
            error={parsed.ok ? undefined : 'Invalid JSON'}
          />

          {!parsed.ok && (
            <Alert color="red" variant="light" icon={<IconAlertTriangle size={16} />} p="xs">
              <Text size="xs">{parsed.error}</Text>
            </Alert>
          )}

          <Text size="xs" c="dimmed">
            Arrays drive <Code>{'{{#each}}'}</Code> blocks, so an empty one shows the empty
            state. Anything absent keeps rendering as <Code>[name]</Code>.
          </Text>
        </Stack>
      </Popover.Dropdown>
    </Popover>
  );
};

/** Parses the panel's text into data the renderer can use, or undefined. */
export const parseSampleData = (json: string): PreviewData | undefined => {
  if (!json.trim()) return undefined;
  try {
    const data = JSON.parse(json);
    if (data && typeof data === 'object' && !Array.isArray(data)) return data as PreviewData;
  } catch {
    // An unparseable draft simply falls back to placeholders rather than
    // blanking the preview while it is being typed.
  }
  return undefined;
};
