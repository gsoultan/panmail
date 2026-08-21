import React from 'react';
import { Badge, Box, Group, NumberInput, Stack, Text } from '@mantine/core';
import { IconAlertTriangle } from '@tabler/icons-react';
import {
  RETENTION_FIELDS,
  RETENTION_GROUPS,
  RETENTION_MAX_DAYS,
  RETENTION_MIN_DAYS,
  describeRetention,
  type RetentionField,
  type RetentionGroup,
  type RetentionKey,
} from './retentionFields';

export interface RetentionSettingsProps {
  values: Record<RetentionKey, number>;
  onChange: (key: RetentionKey, value: number) => void;
  disabled?: boolean;
}

const GROUP_ORDER: RetentionGroup[] = ['delivery', 'queues', 'system'];

/**
 * One number per class of data, and what that number does to it.
 *
 * The descriptions are long on purpose. Every field here deletes something an
 * operator cannot get back, and the difference between "delivery events" and
 * "message content" is not guessable from the label — the first is archived on
 * its way out, the second is gone. A retention page that needs the manual open
 * beside it is a retention page nobody sets correctly.
 */
export const RetentionSettings: React.FC<RetentionSettingsProps> = ({
  values,
  onChange,
  disabled,
}) => (
  <Stack gap="xl">
    {GROUP_ORDER.map((group) => (
      <Box key={group}>
        <Text fw={700} size="sm">
          {RETENTION_GROUPS[group].title}
        </Text>
        <Text size="xs" c="dimmed" mb="md">
          {RETENTION_GROUPS[group].blurb}
        </Text>

        <Stack gap="lg">
          {RETENTION_FIELDS.filter((field) => field.group === group).map((field) => (
            <RetentionInput
              key={field.key}
              field={field}
              value={values[field.key] ?? 0}
              onChange={onChange}
              disabled={disabled}
            />
          ))}
        </Stack>
      </Box>
    ))}
  </Stack>
);

interface RetentionInputProps {
  field: RetentionField;
  value: number;
  onChange: (key: RetentionKey, value: number) => void;
  disabled?: boolean;
}

const RetentionInput: React.FC<RetentionInputProps> = ({ field, value, onChange, disabled }) => {
  const keepsForever = value <= 0;

  return (
    <NumberInput
      label={
        <Group gap="xs" wrap="nowrap">
          <Text size="sm" fw={600}>
            {field.label}
          </Text>
          {keepsForever ? (
            <Badge size="xs" variant="light" color="gray">
              Kept forever
            </Badge>
          ) : null}
          {field.destructive && !keepsForever ? (
            <Badge
              size="xs"
              variant="light"
              color="orange"
              leftSection={<IconAlertTriangle size={10} />}
            >
              Deletes content
            </Badge>
          ) : null}
        </Group>
      }
      description={
        <>
          {field.description}{' '}
          <Text span size="xs" fw={600} c={keepsForever ? 'dimmed' : undefined}>
            {describeRetention(value, field)}
          </Text>
        </>
      }
      // Days, and zero is a real setting rather than an empty field: it is how
      // an operator says "keep forever", so it must be typeable and must not
      // be corrected to the minimum of one that a length usually implies.
      suffix={value === 1 ? ' day' : ' days'}
      min={RETENTION_MIN_DAYS}
      max={RETENTION_MAX_DAYS}
      clampBehavior="strict"
      allowDecimal={false}
      allowNegative={false}
      disabled={disabled}
      value={value}
      onChange={(next) => onChange(field.key, toDays(next))}
    />
  );
};

/**
 * Mantine hands back a string while the field is being edited, and an empty
 * one when it has been cleared. Cleared reads as zero — the same as forever —
 * rather than NaN, which would post a field the server has to reject.
 */
const toDays = (next: string | number): number => {
  const parsed = typeof next === 'number' ? next : Number.parseInt(next, 10);
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 0;
};
