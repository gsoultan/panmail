import DOMPurify from 'dompurify';

/**
 * Sanitiser for the one place the builder renders authored HTML into its own
 * document: the text block's preview.
 *
 * The threat is not the recipient — mail clients strip scripts, and the
 * generated email is never executed here. It is the next person to open the
 * template. Templates are tenant-scoped and an Editor can create them, so a
 * `<script>` typed into the "Text (HTML)" box runs in the session of whichever
 * Administrator opens that template next, with their token in memory. That is a
 * privilege escalation inside the tenant, and it is why this is not optional.
 *
 * The allow-list is deliberately narrow: what an email can actually render.
 * Anything an email client would drop anyway is not worth the attack surface.
 */

const ALLOWED_TAGS = [
  'a', 'b', 'blockquote', 'br', 'code', 'div', 'em', 'h1', 'h2', 'h3', 'h4',
  'h5', 'h6', 'hr', 'i', 'img', 'li', 'ol', 'p', 'pre', 's', 'small', 'span',
  'strike', 'strong', 'sub', 'sup', 'table', 'tbody', 'td', 'tfoot', 'th',
  'thead', 'tr', 'u', 'ul',
];

const ALLOWED_ATTR = [
  'align', 'alt', 'bgcolor', 'border', 'cellpadding', 'cellspacing', 'class',
  'colspan', 'dir', 'height', 'href', 'rowspan', 'src', 'style', 'target',
  'title', 'valign', 'width',
];

/**
 * sanitizeHtml returns markup safe to inject into this document.
 *
 * Merge tags survive: `{{name}}` is text, not markup, so the parser leaves it
 * alone. A template author never loses personalisation by being sanitised.
 */
export const sanitizeHtml = (dirty: unknown): string => {
  const input = String(dirty ?? '');
  if (!input) return '';

  // No DOM (a test runner, SSR): fall back to escaping rather than returning
  // markup nothing has inspected. Failing closed is the only safe default for a
  // function whose entire job is to be trusted.
  if (typeof window === 'undefined' || typeof window.document === 'undefined') {
    return input
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
  }

  return DOMPurify.sanitize(input, {
    ALLOWED_TAGS,
    ALLOWED_ATTR,
    // href/src are still filtered by DOMPurify's own URI policy, which rejects
    // javascript: and friends.
    ALLOW_DATA_ATTR: false,
    ADD_ATTR: ['target'],
    FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed', 'form', 'input'],
    FORBID_ATTR: ['srcset', 'formaction', 'xlink:href'],
  });
};
