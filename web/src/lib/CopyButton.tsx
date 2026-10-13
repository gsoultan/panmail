import type { ReactNode } from 'react';
import { useCopy } from './useCopy';

interface CopyButtonProps {
  value: string;
  /** How long `copied` stays true after a successful copy, in milliseconds. */
  timeout?: number;
  children: (payload: { copied: boolean; copy: () => void }) => ReactNode;
}

/**
 * A drop-in for Mantine's CopyButton that also works over plain HTTP.
 *
 * Mantine's version needs `navigator.clipboard`, which a non-HTTPS page does
 * not have, and it fails without telling anyone. This one goes through
 * copyText, and when the copy really cannot happen it says so instead of
 * leaving the user to paste whatever was on the clipboard before.
 */
export function CopyButton({ value, timeout = 1000, children }: CopyButtonProps) {
  const { copied, copy } = useCopy(timeout);
  return <>{children({ copied, copy: () => void copy(value) })}</>;
}
