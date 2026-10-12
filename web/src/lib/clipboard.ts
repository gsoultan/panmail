/**
 * Copies text to the clipboard, resolving to whether it actually landed there.
 *
 * The async Clipboard API only exists in a secure context: HTTPS, or a page
 * served from localhost. A console reached over plain HTTP on a LAN address
 * (http://10.0.0.5:8080) has no `navigator.clipboard` at all, so Mantine's
 * CopyButton silently did nothing and a bare `navigator.clipboard.writeText`
 * threw. The fallback selects the text in an off-screen textarea and runs the
 * legacy copy command, which browsers still honour from a click handler.
 */
export async function copyText(text: string): Promise<boolean> {
  if (typeof navigator !== 'undefined' && navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Permission denied or the document lost focus; the fallback may still work.
    }
  }
  return legacyCopy(text);
}

function legacyCopy(text: string): boolean {
  if (typeof document === 'undefined' || !document.body) return false;

  const previousFocus = document.activeElement as HTMLElement | null;
  // Inside a modal the textarea goes into the dialog, so its focus trap does
  // not pull focus back out before the selection is copied.
  const host = previousFocus?.closest('[role="dialog"]') ?? document.body;

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.top = '0';
  textarea.style.left = '0';
  textarea.style.width = '1px';
  textarea.style.height = '1px';
  textarea.style.opacity = '0';
  textarea.style.pointerEvents = 'none';
  host.appendChild(textarea);

  let ok = false;
  try {
    textarea.focus();
    textarea.select();
    textarea.setSelectionRange(0, text.length);
    ok = typeof document.execCommand === 'function' && document.execCommand('copy');
  } catch {
    ok = false;
  } finally {
    textarea.remove();
    previousFocus?.focus?.();
  }
  return ok;
}
