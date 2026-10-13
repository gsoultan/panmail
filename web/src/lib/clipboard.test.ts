import { afterEach, describe, expect, mock, test } from 'bun:test';
import { copyText } from './clipboard';

const nav = navigator as unknown as Record<string, unknown>;
const win = window as unknown as Record<string, unknown>;
const doc = document as unknown as Record<string, unknown>;

const original = {
  clipboard: Object.getOwnPropertyDescriptor(nav, 'clipboard'),
  isSecureContext: Object.getOwnPropertyDescriptor(win, 'isSecureContext'),
  execCommand: doc.execCommand,
};

function setClipboard(value: unknown) {
  Object.defineProperty(nav, 'clipboard', { value, configurable: true });
}

function setSecure(value: boolean) {
  Object.defineProperty(win, 'isSecureContext', { value, configurable: true });
}

afterEach(() => {
  if (original.clipboard) Object.defineProperty(nav, 'clipboard', original.clipboard);
  else delete nav.clipboard;
  if (original.isSecureContext) Object.defineProperty(win, 'isSecureContext', original.isSecureContext);
  else delete win.isSecureContext;
  doc.execCommand = original.execCommand;
});

describe('copyText', () => {
  test('uses the Clipboard API in a secure context', async () => {
    const writeText = mock(() => Promise.resolve());
    setClipboard({ writeText });
    setSecure(true);
    const execCommand = mock(() => true);
    doc.execCommand = execCommand;

    expect(await copyText('prov_123')).toBe(true);
    expect(writeText).toHaveBeenCalledWith('prov_123');
    expect(execCommand).not.toHaveBeenCalled();
  });

  // The reported bug: a console served over plain HTTP on a LAN address has
  // no navigator.clipboard, and the copy buttons did nothing.
  test('falls back to the copy command over plain HTTP', async () => {
    setClipboard(undefined);
    setSecure(false);
    let selected = '';
    doc.execCommand = mock((command: string) => {
      const active = document.activeElement as HTMLTextAreaElement;
      selected = active.value.slice(active.selectionStart, active.selectionEnd);
      return command === 'copy';
    });

    expect(await copyText('pm_live_secret')).toBe(true);
    expect(selected).toBe('pm_live_secret');
    expect(document.querySelectorAll('textarea')).toHaveLength(0);
  });

  test('falls back when the Clipboard API rejects', async () => {
    setClipboard({ writeText: () => Promise.reject(new Error('denied')) });
    setSecure(true);
    doc.execCommand = mock(() => true);

    expect(await copyText('x')).toBe(true);
    expect(doc.execCommand).toHaveBeenCalledWith('copy');
  });

  test('reports failure when nothing can copy', async () => {
    setClipboard(undefined);
    setSecure(false);
    doc.execCommand = mock(() => false);

    expect(await copyText('x')).toBe(false);
  });

  test('puts the textarea inside an open dialog so its focus trap keeps it', async () => {
    setClipboard(undefined);
    setSecure(false);
    const dialog = document.createElement('div');
    dialog.setAttribute('role', 'dialog');
    const button = document.createElement('button');
    dialog.appendChild(button);
    document.body.appendChild(dialog);
    button.focus();

    const seen: { parent: Element | null } = { parent: null };
    doc.execCommand = mock(() => {
      seen.parent = document.activeElement?.parentElement ?? null;
      return true;
    });

    expect(await copyText('x')).toBe(true);
    expect(seen.parent).toBe(dialog);
    expect(document.activeElement).toBe(button);
    dialog.remove();
  });
});
