import { Block, EmailDesign } from './types';

/**
 * Renders a design as the plain-text alternative of a multipart message.
 *
 * A visually built template previously shipped HTML only. That costs twice:
 * spam filters score a message with no text/plain part worse than the same
 * message with one, and a reader on a text-only client, a screen reader in
 * plain-text mode, or a watch gets nothing but the fallback the client invents.
 *
 * The output is not a transcription of the markup — it is what the message
 * says. Links are written as "text (url)" because a bare anchor label loses the
 * destination entirely once the markup is gone, and a bare URL loses the reason
 * to click it.
 *
 * Merge tags pass through untouched, so the text part personalises exactly as
 * the HTML part does.
 */

// Wrapping at 78 characters is the long-standing convention for text/plain
// (RFC 5322 counsels 78, requires 998). Beyond that, clients that do not soft
// wrap show one very long line.
const WRAP_COLUMNS = 78;

/** Strips tags and decodes the handful of entities the builder can produce. */
const htmlToText = (html: string): string => {
  if (!html) return '';
  return html
    // Block-level boundaries become line breaks before tags are removed, or
    // paragraphs run together into one wall of text.
    .replace(/<\s*br\s*\/?\s*>/gi, '\n')
    .replace(/<\s*\/\s*(p|div|h[1-6]|li|tr|blockquote)\s*>/gi, '\n')
    .replace(/<\s*li[^>]*>/gi, '  - ')
    .replace(/<[^>]+>/g, '')
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
    .replace(/&quot;/gi, '"')
    .replace(/&#39;/gi, "'")
    // Collapse the runs of blank lines that stripping tags leaves behind.
    .replace(/[ \t]+\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
};

/** Extracts "label (href)" pairs so a link's destination survives. */
const linksFrom = (html: string): string[] => {
  const out: string[] = [];
  const re = /<a\b[^>]*href\s*=\s*["']([^"']+)["'][^>]*>([\s\S]*?)<\/a>/gi;
  let m: RegExpExecArray | null;
  while ((m = re.exec(html)) !== null) {
    const href = m[1].trim();
    const label = htmlToText(m[2]).replace(/\s+/g, ' ').trim();
    if (!href || href === '#') continue;
    out.push(label && label !== href ? `${label}: ${href}` : href);
  }
  return out;
};

const wrap = (text: string, columns = WRAP_COLUMNS): string =>
  text
    .split('\n')
    .map((line) => {
      if (line.length <= columns) return line;
      const words = line.split(' ');
      const lines: string[] = [];
      let current = '';
      for (const word of words) {
        // A single word longer than the limit (a URL, usually) is left intact:
        // breaking it would make it unclickable, which is worse than a long line.
        if (current && current.length + word.length + 1 > columns) {
          lines.push(current);
          current = word;
        } else {
          current = current ? `${current} ${word}` : word;
        }
      }
      if (current) lines.push(current);
      return lines.join('\n');
    })
    .join('\n');

const blockToText = (block: Block, depth = 0): string => {
  if (!block || depth > 10) return '';
  const c = block.content ?? {};

  switch (block.type) {
    case 'heading': {
      const text = htmlToText(String(c.text ?? ''));
      if (!text) return '';
      // Underlined so the structure survives, which is what a heading is for.
      return `${text}\n${'='.repeat(Math.min(text.length, WRAP_COLUMNS))}`;
    }

    case 'text': {
      const body = htmlToText(String(c.text ?? ''));
      const links = linksFrom(String(c.text ?? ''));
      // Links are listed after the paragraph rather than inline, so the prose
      // stays readable and no destination is lost.
      return links.length ? `${body}\n\n${links.join('\n')}` : body;
    }

    case 'button': {
      const label = String(c.label ?? '').trim();
      const url = String(c.url ?? '').trim();
      if (!url || url === '#') return label;
      return label ? `${label}: ${url}` : url;
    }

    case 'image': {
      // An image with no alt text contributes nothing here, which is the same
      // thing a screen reader experiences.
      const alt = String(c.alt ?? '').trim();
      const link = String(c.linkUrl ?? '').trim();
      if (!alt) return link ? link : '';
      return link ? `[${alt}]: ${link}` : `[${alt}]`;
    }

    case 'divider':
      return '-'.repeat(WRAP_COLUMNS);

    case 'spacer':
      return '';

    case 'list': {
      const items: string[] = Array.isArray(c.items) ? c.items : [];
      if (c.loopVariable) {
        const item = items[0] ?? `{{${c.loopItemVariable || 'item'}}}`;
        return `{{#each ${c.loopVariable}}}\n  - ${htmlToText(String(item))}\n{{/each}}`;
      }
      return items.map((i) => `  - ${htmlToText(String(i))}`).join('\n');
    }

    case 'social': {
      const links: any[] = Array.isArray(c.links) ? c.links : [];
      return links
        .filter((l) => l?.url && l.url !== '#')
        .map((l) => `${l.platform || 'Link'}: ${l.url}`)
        .join('\n');
    }

    case 'video': {
      const url = String(c.url ?? '').trim();
      return url ? `Watch the video: ${url}` : '';
    }

    case 'table': {
      const headers: string[] = Array.isArray(c.headers) ? c.headers : [];
      const rows: string[][] = Array.isArray(c.rows) ? c.rows : [];
      const lines: string[] = [];
      if (headers.length) lines.push(headers.map((h) => htmlToText(String(h))).join(' | '));
      if (c.loopVariable) {
        const template = (rows[0] ?? []).map((cell) => htmlToText(String(cell))).join(' | ');
        lines.push(`{{#each ${c.loopVariable}}}`, template, '{{/each}}');
      } else {
        for (const row of rows) {
          lines.push((row ?? []).map((cell) => htmlToText(String(cell))).join(' | '));
        }
      }
      return lines.join('\n');
    }

    case 'columns': {
      const cols: any[] = Array.isArray(c.columns) ? c.columns : [];
      // Columns are a visual arrangement with no meaning in text, so they are
      // flattened in reading order rather than being faked with padding.
      return cols
        .flatMap((col) => (col?.blocks ?? []).map((b: Block) => blockToText(b, depth + 1)))
        .filter(Boolean)
        .join('\n\n');
    }

    default:
      return '';
  }
};

export const generatePlainText = (design: EmailDesign): string => {
  const blocks = design?.blocks ?? [];

  const parts = blocks
    .map((b) => {
      const text = blockToText(b);
      if (!text) return '';
      // A conditional block has to stay conditional in the text part too, or
      // the two halves of the message disagree.
      return b.content?.ifVariable
        ? `{{#if ${b.content.ifVariable}}}\n${text}\n{{/if}}`
        : text;
    })
    .filter(Boolean);

  const preheader = design?.preheader?.trim();
  if (preheader) parts.unshift(preheader);

  return wrap(parts.join('\n\n')).replace(/\n{3,}/g, '\n\n').trim();
};
