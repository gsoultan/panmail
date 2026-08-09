import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { TextInput, Textarea, Popover, ScrollArea, Text, Group, Kbd, Code, Stack } from '@mantine/core';
import { activeToken, filterVariables, insertVariable } from './variableSuggest';

interface VariableInputProps {
  label?: React.ReactNode;
  placeholder?: string;
  description?: React.ReactNode;
  value: string;
  onChange: (value: string) => void;
  /** Names offered, without braces. */
  variables: string[];
  multiline?: boolean;
  minRows?: number;
  size?: string;
  autosize?: boolean;
  maxRows?: number;
}

/** How many suggestions to show before scrolling. */
const VISIBLE = 8;

/**
 * A text field that completes merge tags as they are typed.
 *
 * Inserting a variable used to mean stopping, opening a popover, finding the
 * name and clicking it — slower than typing `{{name}}` by hand, so the list
 * went mostly unused and names got misspelled instead, which surfaces as an
 * empty gap in a sent message. Typing `{{` now opens the same list, filtered as
 * the name is typed and accepted from the keyboard, so the fast path and the
 * correct path are the same path.
 */
export const VariableInput: React.FC<VariableInputProps> = ({
  label, placeholder, description, value, onChange, variables,
  multiline, minRows, size = 'xs', autosize, maxRows,
}) => {
  const ref = useRef<HTMLInputElement & HTMLTextAreaElement>(null);
  const [caret, setCaret] = useState(0);
  const [highlighted, setHighlighted] = useState(0);
  // Dismissing has to be sticky until the tag changes, or Escape would reopen
  // the list on the very next keystroke.
  const [dismissedAt, setDismissedAt] = useState<number | null>(null);

  const token = useMemo(() => activeToken(value, caret), [value, caret]);
  const matches = useMemo(
    () => (token ? filterVariables(variables, token.query) : []),
    [token, variables],
  );

  const open = Boolean(token) && matches.length > 0 && dismissedAt !== token!.start;

  // Once the caret leaves the dismissed tag there is nothing left to suppress,
  // and holding the index would silence a later tag that happens to begin at
  // the same offset.
  useEffect(() => {
    if (!token && dismissedAt !== null) setDismissedAt(null);
  }, [token, dismissedAt]);

  const accept = useCallback((variable: string) => {
    if (!token) return;
    const next = insertVariable(value, token, variable);
    onChange(next.text);
    setDismissedAt(null);

    // The caret has to be restored after React writes the new value, or it
    // jumps to the end and the author loses their place mid-sentence.
    requestAnimationFrame(() => {
      const el = ref.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(next.caret, next.caret);
      setCaret(next.caret);
    });
  }, [token, value, onChange]);

  const syncCaret = (e: React.SyntheticEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    setCaret(e.currentTarget.selectionStart ?? 0);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    if (!open) return;

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setHighlighted((i) => (i + 1) % matches.length);
        return;
      case 'ArrowUp':
        e.preventDefault();
        setHighlighted((i) => (i - 1 + matches.length) % matches.length);
        return;
      case 'Enter':
      case 'Tab':
        // Enter in a multiline field would otherwise insert a newline behind
        // the open list, which reads as the suggestion being ignored.
        e.preventDefault();
        accept(matches[Math.min(highlighted, matches.length - 1)]);
        return;
      case 'Escape':
        e.preventDefault();
        setDismissedAt(token!.start);
        return;
      default:
        // Any other key changes the query, so start again from the top match.
        setHighlighted(0);
    }
  };

  const shared = {
    ref,
    label,
    placeholder,
    description,
    size,
    value,
    onChange: (e: React.ChangeEvent<HTMLInputElement & HTMLTextAreaElement>) => {
      onChange(e.currentTarget.value);
      setCaret(e.currentTarget.selectionStart ?? 0);
      // Deliberately not clearing the dismissal here. Doing so reopened the
      // list on the very next character, which made Escape useless: the author
      // dismisses it, keeps typing the name by hand, and Enter inserts a
      // suggestion over what they wrote.
    },
    onKeyDown,
    onClick: syncCaret,
    onKeyUp: syncCaret,
    onSelect: syncCaret,
  };

  return (
    <Popover
      opened={open}
      position="bottom-start"
      shadow="md"
      width={280}
      withinPortal
      // No animation. A completion list that fades in reads as lag when it is
      // meant to keep up with typing, and it should disappear the instant the
      // tag is finished rather than lingering over the next word.
      transitionProps={{ duration: 0 }}
      // Focus must stay in the field: the list is a hint, not a destination.
      trapFocus={false}
      returnFocus={false}
    >
      <Popover.Target>
        <div>
          {multiline
            ? <Textarea {...shared} minRows={minRows} autosize={autosize} maxRows={maxRows} />
            : <TextInput {...shared} />}
        </div>
      </Popover.Target>

      <Popover.Dropdown p={4}>
        <ScrollArea.Autosize mah={VISIBLE * 30}>
          <Stack gap={0}>
            {matches.map((v, i) => (
              <Group
                key={v}
                gap="xs"
                px="xs"
                py={4}
                wrap="nowrap"
                style={{
                  cursor: 'pointer',
                  borderRadius: 4,
                  backgroundColor: i === highlighted
                    ? 'light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-5))'
                    : undefined,
                }}
                onMouseEnter={() => setHighlighted(i)}
                // mousedown, not click: click fires after blur, by which point
                // the caret position the insertion depends on is gone.
                onMouseDown={(e) => { e.preventDefault(); accept(v); }}
              >
                <Code style={{ fontSize: 11 }}>{`{{${v}}}`}</Code>
              </Group>
            ))}
          </Stack>
        </ScrollArea.Autosize>
        <Group gap={6} px="xs" pt={4} pb={2} justify="flex-end">
          <Text size="10px" c="dimmed"><Kbd size="xs">↑↓</Kbd> move</Text>
          <Text size="10px" c="dimmed"><Kbd size="xs">↵</Kbd> insert</Text>
        </Group>
      </Popover.Dropdown>
    </Popover>
  );
};
