import { Block, EmailDesign } from './types';

/**
 * Checks a design for the things that quietly hurt a campaign.
 *
 * None of these stop a message being sent, and none of them are visible in the
 * preview — which is exactly why they need surfacing. An image with no alt text
 * looks perfect until the recipient's client blocks images, which most do by
 * default. An image-only message is a classic spam signature. A missing
 * preheader hands the client the first words of the body to show after the
 * subject, whatever those happen to be.
 *
 * Severity is meant honestly: `error` is something that will visibly fail for
 * some readers, `warning` is something that measurably costs deliverability or
 * engagement, `info` is a suggestion.
 */

export type LintSeverity = 'error' | 'warning' | 'info';

export interface LintFinding {
  id: string;
  severity: LintSeverity;
  title: string;
  /** What actually goes wrong, in terms of what the reader experiences. */
  detail: string;
  /** The block to select when the finding is clicked, when there is one. */
  blockId?: string;
}

/** Walks every block, including those nested inside columns. */
const walk = (blocks: Block[], fn: (b: Block) => void, depth = 0): void => {
  if (depth > 10) return;
  for (const b of blocks) {
    if (!b) continue;
    fn(b);
    if (b.type === 'columns' && Array.isArray(b.content?.columns)) {
      for (const col of b.content.columns) {
        walk(col?.blocks ?? [], fn, depth + 1);
      }
    }
  }
};

const isPlaceholderUrl = (url: unknown): boolean => {
  const s = String(url ?? '').trim();
  return s === '' || s === '#' || s === 'https://' || s === 'http://';
};

export const lintDesign = (design: EmailDesign): LintFinding[] => {
  const findings: LintFinding[] = [];
  const blocks = design?.blocks ?? [];

  let imageCount = 0;
  let textCharacters = 0;
  let hasUnsubscribeLink = false;

  walk(blocks, (b) => {
    const c = b.content ?? {};

    switch (b.type) {
      case 'image': {
        imageCount++;
        if (!String(c.src ?? '').trim()) {
          findings.push({
            id: `image-no-src-${b.id}`,
            severity: 'error',
            title: 'Image has no source',
            detail: 'This block renders as a broken image icon. Add a URL or remove the block.',
            blockId: b.id,
          });
        } else if (!String(c.alt ?? '').trim()) {
          findings.push({
            id: `image-no-alt-${b.id}`,
            severity: 'warning',
            title: 'Image has no alt text',
            detail:
              'Most clients block images by default, so this is what many readers see instead of the picture — nothing. Screen readers skip it too.',
            blockId: b.id,
          });
        }
        break;
      }

      case 'button': {
        if (isPlaceholderUrl(c.url)) {
          findings.push({
            id: `button-no-url-${b.id}`,
            severity: 'error',
            title: 'Button goes nowhere',
            detail: 'The link is still a placeholder, so clicking it does nothing.',
            blockId: b.id,
          });
        }
        if (!String(c.label ?? '').trim()) {
          findings.push({
            id: `button-no-label-${b.id}`,
            severity: 'error',
            title: 'Button has no label',
            detail: 'The button renders as an empty coloured box.',
            blockId: b.id,
          });
        }
        break;
      }

      case 'heading':
        textCharacters += String(c.text ?? '').trim().length;
        break;

      case 'text': {
        const plain = String(c.text ?? '').replace(/<[^>]+>/g, '').trim();
        textCharacters += plain.length;
        if (/\{\{\s*unsubscribe_url\s*\}\}/.test(String(c.text ?? ''))) {
          hasUnsubscribeLink = true;
        }
        break;
      }

      case 'link' as any:
        break;

      default:
        break;
    }

    if (typeof c.url === 'string' && /\{\{\s*unsubscribe_url\s*\}\}/.test(c.url)) {
      hasUnsubscribeLink = true;
    }
  });

  if (blocks.length === 0) {
    findings.push({
      id: 'empty-design',
      severity: 'info',
      title: 'The email is empty',
      detail: 'Add a block from the panel to get started.',
    });
    return findings;
  }

  // A message that is one big image is a long-standing spam signature, and it
  // is unreadable the moment images are blocked.
  if (imageCount > 0 && textCharacters < 100) {
    findings.push({
      id: 'image-heavy',
      severity: 'warning',
      title: 'Almost no text',
      detail:
        'A message that is mostly image is a well-known spam signature, and it is unreadable when images are blocked. Add real text alongside the pictures.',
    });
  }

  if (!design?.preheader?.trim()) {
    findings.push({
      id: 'no-preheader',
      severity: 'warning',
      title: 'No preheader',
      detail:
        'Without one, the client shows whatever the first words of the body happen to be next to your subject line. That is prime space in the inbox.',
    });
  }

  // The header is added automatically at send time, so this is about the
  // visible link recipients look for — its absence reads as evasive.
  if (!hasUnsubscribeLink) {
    findings.push({
      id: 'no-visible-unsubscribe',
      severity: 'info',
      title: 'No visible unsubscribe link',
      detail:
        'The one-click header is added automatically, but bulk mail is also expected to carry a visible link. Insert {{unsubscribe_url}} in a text block.',
    });
  }

  return findings;
};

export const countBySeverity = (findings: LintFinding[]): Record<LintSeverity, number> => {
  const counts: Record<LintSeverity, number> = { error: 0, warning: 0, info: 0 };
  for (const f of findings) counts[f.severity]++;
  return counts;
};
