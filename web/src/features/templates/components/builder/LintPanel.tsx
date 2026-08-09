import React from 'react';
import { Stack, Group, Text, Badge, UnstyledButton, ThemeIcon, Popover, ActionIcon, Tooltip, ScrollArea } from '@mantine/core';
import { IconAlertTriangle, IconCircleX, IconInfoCircle, IconCircleCheck, IconStethoscope } from '@tabler/icons-react';
import { LintFinding, LintSeverity, countBySeverity } from './lint';

interface LintPanelProps {
  findings: LintFinding[];
  onSelectBlock: (blockId: string) => void;
}

const SEVERITY: Record<LintSeverity, { color: string; icon: React.ReactNode; label: string }> = {
  error: { color: 'red', icon: <IconCircleX size={14} />, label: 'Breaks for some readers' },
  warning: { color: 'orange', icon: <IconAlertTriangle size={14} />, label: 'Costs deliverability' },
  info: { color: 'blue', icon: <IconInfoCircle size={14} />, label: 'Suggestion' },
};

/**
 * Surfaces design problems the preview cannot show.
 *
 * It lives in the toolbar as a single badge rather than a panel that is always
 * open: none of these findings block a send, so taking permanent space would
 * make them noise. The badge only draws attention when there is something to
 * say, and clicking a finding selects the block it is about, which is the
 * difference between a warning and a fix.
 */
export const LintPanel: React.FC<LintPanelProps> = ({ findings, onSelectBlock }) => {
  const counts = countBySeverity(findings);
  const clean = findings.length === 0;

  // The badge takes the colour of the worst finding, so a single error is not
  // hidden behind a pile of suggestions.
  const worst: LintSeverity | null =
    counts.error > 0 ? 'error' : counts.warning > 0 ? 'warning' : counts.info > 0 ? 'info' : null;

  return (
    <Popover width={380} position="bottom-end" shadow="md" withArrow>
      <Popover.Target>
        <Tooltip label={clean ? 'No issues found' : `${findings.length} thing${findings.length === 1 ? '' : 's'} to look at`}>
          <ActionIcon
            variant="light"
            color={clean ? 'teal' : SEVERITY[worst!].color}
            aria-label="Design checks"
            pos="relative"
          >
            <IconStethoscope size={18} />
            {!clean && (
              <Badge
                size="xs"
                circle
                color={SEVERITY[worst!].color}
                style={{ position: 'absolute', top: -4, right: -4, pointerEvents: 'none' }}
              >
                {findings.length}
              </Badge>
            )}
          </ActionIcon>
        </Tooltip>
      </Popover.Target>

      <Popover.Dropdown p="xs">
        {clean ? (
          <Group gap="xs" p="sm">
            <ThemeIcon color="teal" variant="light" size="sm"><IconCircleCheck size={14} /></ThemeIcon>
            <div>
              <Text size="sm" fw={600}>Nothing to fix</Text>
              <Text size="xs" c="dimmed">Alt text, links, preheader and text balance all look right.</Text>
            </div>
          </Group>
        ) : (
          <ScrollArea.Autosize mah={360}>
            <Stack gap={4}>
              {findings.map((f) => {
                const s = SEVERITY[f.severity];
                const clickable = Boolean(f.blockId);
                return (
                  <UnstyledButton
                    key={f.id}
                    onClick={() => f.blockId && onSelectBlock(f.blockId)}
                    style={{
                      padding: 8,
                      borderRadius: 6,
                      cursor: clickable ? 'pointer' : 'default',
                    }}
                    aria-label={clickable ? `${f.title} — select the block` : f.title}
                  >
                    <Group gap="xs" wrap="nowrap" align="flex-start">
                      <ThemeIcon color={s.color} variant="light" size="sm" mt={2}>
                        {s.icon}
                      </ThemeIcon>
                      <div style={{ flex: 1 }}>
                        <Group gap={6} wrap="nowrap">
                          <Text size="sm" fw={600}>{f.title}</Text>
                          {clickable && <Text size="xs" c="dimmed">— click to select</Text>}
                        </Group>
                        <Text size="xs" c="dimmed" style={{ lineHeight: 1.45 }}>{f.detail}</Text>
                      </div>
                    </Group>
                  </UnstyledButton>
                );
              })}
            </Stack>
          </ScrollArea.Autosize>
        )}
      </Popover.Dropdown>
    </Popover>
  );
};
