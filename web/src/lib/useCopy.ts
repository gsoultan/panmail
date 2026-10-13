import { useCallback, useEffect, useRef, useState } from 'react';
import { notifications } from '@mantine/notifications';
import { copyText } from './clipboard';

/** The copy-and-flash behaviour behind CopyButton, for buttons that need it directly. */
export function useCopy(timeout = 1000) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  const copy = useCallback(
    async (value: string) => {
      const ok = await copyText(value);
      window.clearTimeout(timer.current);
      if (!ok) {
        setCopied(false);
        notifications.show({
          title: 'Could not copy',
          message: 'Your browser blocked clipboard access. Select the text and copy it manually.',
          color: 'red',
        });
        return false;
      }
      setCopied(true);
      timer.current = window.setTimeout(() => setCopied(false), timeout);
      return true;
    },
    [timeout],
  );

  return { copied, copy };
}
