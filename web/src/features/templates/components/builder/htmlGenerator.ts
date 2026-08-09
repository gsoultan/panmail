import { EmailDesign, Block } from './types';

/**
 * Turns a design into email HTML.
 *
 * Two rules govern everything here.
 *
 * 1. Every interpolated value is escaped, except the `text` block, whose editor
 *    is explicitly labelled "Text (HTML)" and whose preview renders it as HTML.
 *    An unescaped value in an attribute is not a styling nit: an alt text or a
 *    URL containing a double quote closes the attribute early and corrupts the
 *    rest of the tag, and the same hole lets authored content inject markup
 *    into a message every recipient of that template receives.
 *
 * 2. Layout is tables, and anything an email client is known to drop is not
 *    used for anything load-bearing. Gmail strips `position`, Outlook ignores
 *    flexbox, and both collapse an empty `div` that only has a height.
 */

// Escapes text and attribute values alike. `'` is included because single
// quotes are legal attribute delimiters and cheap to cover.
//
// Handlebars expressions survive this untouched — `{{name}}` contains no
// escapable character — so merge tags still reach the renderer intact.
const esc = (value: unknown): string =>
  String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');

// Schemes that execute rather than navigate. Some webmail clients have
// historically honoured them, and the builder's own preview definitely does.
const DANGEROUS_SCHEME = /^\s*(?:javascript|vbscript|data)\s*:/i;

const safeUrl = (url: unknown, fallback = '#'): string => {
  const raw = String(url ?? '').trim();
  if (!raw) return fallback;
  if (DANGEROUS_SCHEME.test(raw)) return fallback;
  return esc(raw);
};

// An image src may legitimately be a data: URI, but never a script-bearing one.
const safeImageUrl = (url: unknown): string => {
  const raw = String(url ?? '').trim();
  if (!raw) return '';
  if (/^\s*(?:javascript|vbscript)\s*:/i.test(raw)) return '';
  return esc(raw);
};

// CSS properties whose values are plain numbers. Everything else gets px when
// handed a number, matching what React does for inline styles — without this a
// spacer configured through the NumberInput emits `height: 20`, which is not
// valid CSS and is silently dropped.
const UNITLESS = new Set([
  'lineHeight', 'fontWeight', 'opacity', 'zIndex', 'flex', 'flexGrow',
  'flexShrink', 'order', 'zoom', 'columnCount', 'fillOpacity', 'strokeOpacity',
]);

// Keys carried on the design for the builder's own use that are not CSS.
const NON_CSS_KEYS = new Set(['contentWidth']);

const cssProperty = (prop: string): string => {
  // Vendor prefixes arrive capitalised (`WebkitTextSizeAdjust`, `msFlex`) and
  // need a leading dash that a naive camel-to-kebab pass does not produce.
  const kebab = prop.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`);
  return /^(webkit|moz|ms|o)-/.test(kebab) ? `-${kebab}` : kebab;
};

export const styleToString = (style: Record<string, unknown> | undefined): string => {
  if (!style) return '';
  return Object.entries(style)
    .filter(([prop, val]) => {
      if (NON_CSS_KEYS.has(prop)) return false;
      // An undefined value used to be stringified into the output as the
      // literal `undefined`, producing declarations the client discards along
      // with, in some parsers, the rest of the attribute.
      return val !== undefined && val !== null && val !== '';
    })
    .map(([prop, val]) => {
      const value = typeof val === 'number' && !UNITLESS.has(prop) ? `${val}px` : String(val);
      return `${cssProperty(prop)}: ${value}`;
    })
    .join('; ');
};

// A length that has to end up in an HTML width attribute, which takes a bare
// number. `600px` becomes 600; a percentage has no pixel equivalent, so the
// standard 600 is used rather than the nonsense `100` that stripping the `%`
// from `100%` produced.
const pixelWidth = (value: string | undefined, fallback = 600): number => {
  if (!value) return fallback;
  if (value.trim().endsWith('%')) return fallback;
  const n = parseInt(value, 10);
  return Number.isFinite(n) && n > 0 ? n : fallback;
};

// The rendered height of a button, which VML needs in order to express a corner
// radius as a percentage.
const BUTTON_HEIGHT_PX = 45;

/**
 * Converts a CSS corner radius into the percentage VML wants.
 *
 * Outlook draws the button with a VML roundrect whose arcsize is a proportion of
 * the shape's smaller dimension, not an absolute length. Passing a doubled pixel
 * value happens to be right at 50px tall and wrong everywhere else.
 */
export const vmlArcsize = (radiusPx: number, heightPx: number): number => {
  if (radiusPx <= 0 || heightPx <= 0) return 0;
  return Math.min(100, Math.round((radiusPx * 100) / heightPx));
};

// Outlook shows roughly this much of the preheader. Longer text is not hidden —
// it spills into the preview line after the subject and pushes out the words
// that were meant to be read.
export const MSO_PREHEADER_MAX = 130;

const truncatePreheader = (text: string): string => {
  const chars = Array.from(text);
  if (chars.length <= MSO_PREHEADER_MAX) return text;
  return chars.slice(0, MSO_PREHEADER_MAX - 1).join('') + '…';
};

// Columns can nest, and a design loaded from storage is not guaranteed to be
// acyclic. A depth cap turns a corrupt design into a truncated email instead of
// a hung browser tab.
const MAX_DEPTH = 10;

/**
 * Breakpoints, shared with the builder's viewport switcher so the preview sizes
 * and the email's own media queries cannot drift apart.
 *
 * These are fixed rather than derived from the design's content width. A
 * 500px-wide email and an 800px-wide one both need to stack on the same phone,
 * and the old single query at the content width meant a narrow design never
 * triggered its mobile rules at all.
 */
export const MOBILE_BREAKPOINT = 600;
export const TABLET_BREAKPOINT = 900;

/** Widths the preview renders at, matching the queries above. */
export const VIEWPORT_WIDTHS = {
  mobile: 375,
  tablet: 768,
  desktop: 1100,
} as const;

export type Viewport = keyof typeof VIEWPORT_WIDTHS;

export const generateHTML = (design: EmailDesign): string => {
  const { blocks = [], bodyStyle = {}, preheader } = design || ({} as EmailDesign);

  const contentWidth = bodyStyle.contentWidth || '600px';
  const widthNumeric = pixelWidth(contentWidth);

  // `contentWidth` is a builder concept, not a CSS property, and used to be
  // spread straight into the body's style attribute as `content-width: 600px`.
  const { contentWidth: _ignored, ...bodyCss } = bodyStyle;
  const bodyStyles = styleToString({
    margin: 0,
    padding: 0,
    width: '100%',
    backgroundColor: '#f4f4f4',
    fontFamily: 'Inter, Helvetica, Arial, sans-serif',
    ...bodyCss,
  });

  const content = blocks.map((b) => renderBlockToHTML(b, 0, widthNumeric)).join('\n');

  const preheaderHtml = preheader
    ? `
    <div style="display: none; max-height: 0px; overflow: hidden;">
      ${esc(truncatePreheader(preheader))}
    </div>
    <!-- Spacer characters stop the client pulling body copy into the preview line -->
    <div style="display: none; max-height: 0px; overflow: hidden;">
      &zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;
    </div>
  `
    : '';

  return `
<!DOCTYPE html>
<!--gsmail:outlook-->
<html lang="en" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="X-UA-Compatible" content="IE=edge">
  <meta name="color-scheme" content="light dark">
  <meta name="supported-color-schemes" content="light dark">
  <title>${esc(preheader ? truncatePreheader(preheader) : 'Email')}</title>
  <!--[if mso]>
  <xml>
    <o:OfficeDocumentSettings>
      <o:AllowPNG/>
      <o:PixelsPerInch>96</o:PixelsPerInch>
    </o:OfficeDocumentSettings>
  </xml>
  <![endif]-->
  <style type="text/css">
    body, table, td, a { -webkit-text-size-adjust: 100%; -ms-text-size-adjust: 100%; }
    table, td { mso-table-lspace: 0pt; mso-table-rspace: 0pt; }
    img { -ms-interpolation-mode: bicubic; }
    img { border: 0; line-height: 100%; outline: none; text-decoration: none; display: block; }
    table { border-collapse: collapse !important; }
    /* Word's engine treats line-height as a minimum and grows it to fit the
       font's own metrics, so text set at 1.5 comes out noticeably looser in
       Outlook than everywhere else and carefully spaced blocks drift apart.
       The exactly rule makes it honour the figure it was given. It has to be
       declared wherever a line-height lands, which is why it also appears
       inline below: Outlook applies an inline line-height without inheriting
       the mode set here. */
    body, table, td, p, a, li, blockquote { mso-line-height-rule: exactly; }
    /* Word's engine indents list items by an extra em, so a bulleted list sits
       further right in Outlook than anywhere else. Ported from gsmail's
       outlook package, which is where this knowledge is maintained. */
    li { text-indent: -1em; }
    body { height: 100% !important; margin: 0 !important; padding: 0 !important; width: 100% !important; }
    div[style*="margin: 16px 0;"] { margin: 0 !important; }

    /* Images scale with their container everywhere but Outlook, which uses the
       width attribute instead and ignores this. */
    img.fluid { width: 100% !important; max-width: 100% !important; height: auto !important; }

    /* Tablet. The shell is already fluid via max-width, so this only trims the
       generous desktop gutter — at 768px a 30px inset on each side costs an
       eighth of the readable width. */
    @media screen and (max-width: ${TABLET_BREAKPOINT}px) {
      .email-container { width: 100% !important; max-width: 100% !important; }
      .content-padding { padding-left: 24px !important; padding-right: 24px !important; }
    }

    /* Phone. Columns stop being columns, the gutter shrinks again, headings come
       down to a size that does not wrap after two words, and buttons go full
       width so they are thumb-sized rather than pixel-hunting. */
    @media screen and (max-width: ${MOBILE_BREAKPOINT}px) {
      .email-container { width: 100% !important; max-width: 100% !important; border-radius: 0 !important; }
      .content-padding { padding: 24px 16px !important; }
      .stack-column { display: block !important; width: 100% !important; max-width: 100% !important; direction: ltr !important; padding-left: 0 !important; padding-right: 0 !important; }
      .stack-column + .stack-column { padding-top: 16px !important; }
      .mobile-full-width { width: 100% !important; max-width: 100% !important; }
      .mobile-center { text-align: center !important; }
      h1 { font-size: 24px !important; line-height: 1.25 !important; }
      h2 { font-size: 20px !important; line-height: 1.3 !important; }
      h3 { font-size: 18px !important; line-height: 1.35 !important; }
      /* Below about 16px iOS Safari zooms the whole message to compensate. */
      .body-text, .body-text p, .body-text div, td, li { font-size: 16px !important; line-height: 1.5 !important; }
      /* A wide data table cannot stack, so let it scroll rather than force the
         whole message wider than the screen. */
      .data-table-wrap { overflow-x: auto !important; -webkit-overflow-scrolling: touch !important; }
    }
  </style>
</head>
<body style="${bodyStyles}">
  ${preheaderHtml}
  <table role="presentation" border="0" cellpadding="0" cellspacing="0" width="100%" style="mso-table-lspace:0pt;mso-table-rspace:0pt;border-collapse:collapse;">
    <tr>
      <td align="center" class="content-padding" style="padding: 20px 10px;">
        <!--[if mso]>
        <table role="presentation" align="center" border="0" cellspacing="0" cellpadding="0" width="${widthNumeric}">
        <tr>
        <td align="center" valign="top" width="${widthNumeric}">
        <![endif]-->
        <!-- width="100%" with a max-width is what makes this fluid on a phone.
             A fixed width attribute would pin the message wider than the screen
             and force horizontal scrolling; Outlook, which ignores max-width,
             gets its fixed width from the mso table above. -->
        <table role="presentation" border="0" cellpadding="0" cellspacing="0" width="100%" class="email-container" style="width: 100%; max-width: ${widthNumeric}px; background-color: #ffffff; border-radius: 8px; overflow: hidden; box-shadow: 0 4px 6px rgba(0,0,0,0.05); mso-table-lspace:0pt;mso-table-rspace:0pt;border-collapse:collapse;">
          <tr>
            <td class="content-padding body-text" style="padding: 40px 30px;">
              ${content}
            </td>
          </tr>
        </table>
        <!--[if mso]>
        </td>
        </tr>
        </table>
        <![endif]-->
      </td>
    </tr>
  </table>
</body>
</html>
  `.trim();
};

// containerWidth is the pixel width the block is laid out in. VML cannot size
// itself to its content, so a background image has to be given a box, and the
// only honest number available is the width the email is built at.
const renderBlockToHTML = (block: Block, depth = 0, containerWidth = 600): string => {
  if (!block || depth > MAX_DEPTH) return '';

  const styles = styleToString(block.style as Record<string, unknown>);
  let html = '';

  switch (block.type) {
    case 'heading': {
      // Only the six real heading levels; anything else would be injected as a
      // tag name straight out of stored content.
      const requested = String(block.content.level || 'h1').toLowerCase();
      const level = /^h[1-6]$/.test(requested) ? requested : 'h1';
      html = `<${level} style="${styles}">${esc(block.content.text)}</${level}>`;
      break;
    }

    // The one deliberate exception to escaping: the editor for this block is
    // labelled "Text (HTML)" and the preview renders it as HTML, so authors
    // expect their markup to survive.
    case 'text':
      html = `<div style="${styles}">${block.content.text ?? ''}</div>`;
      break;

    case 'button': {
      const btnColor = block.style.color || '#ffffff';
      const btnBg = block.style.backgroundColor || '#0073ea';
      const btnRadius = block.style.borderRadius || '4px';
      const btnAlign = block.style.textAlign || 'center';
      const btnWidth = block.style.width || 'auto';
      const btnWidthMso = btnWidth === '100%' ? '500px' : '200px';
      // VML arcsize is a percentage of the button's smaller dimension, so it
      // depends on the height. The old formula doubled the radius, which is
      // only correct for a 50px-tall button and rounded every other size wrong
      // — a 6px radius on this 45px button came out as 12% instead of 13%.
      const arcsize = `${vmlArcsize(parseInt(String(btnRadius), 10) || 0, BUTTON_HEIGHT_PX)}%`;
      const href = safeUrl(block.content.url);
      const label = esc(block.content.label);
      const fontSize = block.style.fontSize || '16px';
      const fontWeight = block.style.fontWeight || 'bold';

      html = `
        <table role="presentation" border="0" cellpadding="0" cellspacing="0" class="mobile-full-width" style="margin: 20px 0; width: ${esc(btnWidth)}; border-collapse: separate !important;">
          <tr>
            <td align="${esc(btnAlign)}" class="mobile-center">
              <div>
                <!--[if mso]>
                <v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="${href}" style="height:45px;v-text-anchor:middle;width:${btnWidthMso};" arcsize="${arcsize}" stroke="f" fillcolor="${esc(btnBg)}">
                  <w:anchorlock/>
                  <center style="color:${esc(btnColor)};font-family:sans-serif;font-size:${esc(fontSize)};font-weight:${esc(fontWeight)};">${label}</center>
                </v:roundrect>
                <![endif]-->
                <a href="${href}" target="_blank" class="mobile-full-width" style="background-color:${esc(btnBg)};border-radius:${esc(btnRadius)};color:${esc(btnColor)};display:inline-block;font-family:sans-serif;font-size:${esc(fontSize)};font-weight:${esc(fontWeight)};line-height:45px;mso-line-height-rule:exactly;min-height:45px;text-align:center;text-decoration:none;width:${esc(btnWidth)};padding: 0 24px;box-sizing:border-box;-webkit-text-size-adjust:none;mso-hide:all;">
                  ${label}
                </a>
              </div>
            </td>
          </tr>
        </table>
      `;
      break;
    }

    case 'image': {
      const src = safeImageUrl(block.content.src);
      // An empty src renders as a broken-image icon in every client, which
      // looks worse than the block simply not being there yet.
      if (!src) {
        html = '';
        break;
      }
      const imgWidth = pixelWidth(block.style.width as string, 600);
      // The width attribute is for Outlook, which ignores max-width; the
      // `fluid` class is what lets every other client shrink the image to the
      // screen instead of forcing the message to scroll sideways.
      const imgHtml = `<img src="${src}" alt="${esc(block.content.alt)}" width="${imgWidth}" border="0" class="fluid" style="display: block; width: 100%; max-width: ${imgWidth}px; height: auto; border-radius: ${esc(block.style.borderRadius || '4px')}; ${styles}" />`;
      const linkUrl = String(block.content.linkUrl ?? '').trim();
      const imgContent = linkUrl
        ? `<a href="${safeUrl(linkUrl)}" target="_blank">${imgHtml}</a>`
        : imgHtml;
      html = `
        <table role="presentation" border="0" cellpadding="0" cellspacing="0" width="100%" style="margin: 20px 0;">
          <tr>
            <td align="${esc(block.style.textAlign || 'center')}">
              ${imgContent}
            </td>
          </tr>
        </table>
      `;
      break;
    }

    case 'divider': {
      const count = Math.max(1, Math.min(10, Number(block.content.count) || 1));
      const spacing = Number(block.content.spacing) || 5;
      const hrStyle = `border: 0; border-top: ${esc(block.style.borderTopWidth || '1px')} ${esc(block.style.borderTopStyle || 'solid')} ${esc(block.style.borderTopColor || '#eeeeee')}; margin: 0;`;

      let dividers = '';
      for (let i = 0; i < count; i++) {
        dividers += `<hr style="${hrStyle}${i > 0 ? ` margin-top: ${spacing}px;` : ''}" />`;
      }
      html = `<div style="margin: 30px 0; ${styles}">${dividers}</div>`;
      break;
    }

    case 'spacer': {
      // A div with only a height collapses in Outlook and is dropped by some
      // Gmail views. A table cell with a matching height, line-height and a
      // non-breaking space is the shape that survives everywhere.
      const raw = block.content.height ?? 20;
      const px = typeof raw === 'number' ? raw : parseInt(String(raw), 10) || 20;
      html = `
        <table role="presentation" border="0" cellpadding="0" cellspacing="0" width="100%">
          <tr>
            <td height="${px}" style="height: ${px}px; line-height: ${px}px; mso-line-height-rule: exactly; font-size: 0; ${styles}">&nbsp;</td>
          </tr>
        </table>
      `;
      break;
    }

    case 'list': {
      if (block.content.loopVariable) {
        const itemAlias = esc(block.content.loopItemVariable || 'item');
        const template = esc(block.content.items?.[0] ?? `{{${block.content.loopItemVariable || 'item'}}}`);
        html = `
          <ul style="${styles}">
            {{#each ${esc(block.content.loopVariable)}}}
              <li>${template || `{{${itemAlias}}}`}</li>
            {{/each}}
          </ul>
        `;
      } else {
        const itemsList = (block.content.items || [])
          .map((item: string) => `<li>${esc(item)}</li>`)
          .join('');
        html = `<ul style="${styles}">${itemsList}</ul>`;
      }
      break;
    }

    case 'social': {
      const icons = (block.content.links || [])
        .map((link: any) => {
          const icon = safeImageUrl(link?.icon);
          if (!icon) return '';
          return `
        <td style="padding: 0 5px;">
          <a href="${safeUrl(link?.url)}" target="_blank">
            <img src="${icon}" alt="${esc(link?.platform ?? '')}" width="32" height="32" style="display: block; border: 0;" />
          </a>
        </td>
      `;
        })
        .join('');
      html = icons
        ? `
        <table role="presentation" border="0" cellpadding="0" cellspacing="0" align="${esc(block.style.textAlign || 'center')}">
          <tr>${icons}</tr>
        </table>
      `
        : '';
      break;
    }

    case 'video': {
      // The old markup centred a play badge with position:absolute, transform
      // and flexbox. Gmail strips positioning and Outlook ignores flex, so the
      // badge landed under the thumbnail as a stray black square in exactly the
      // clients most recipients use. A linked thumbnail with the play
      // affordance on its own row renders identically everywhere.
      const thumb = safeImageUrl(block.content.thumbnail);
      const href = safeUrl(block.content.url);
      const align = esc(block.style.textAlign || 'center');
      const thumbHtml = thumb
        ? `<a href="${href}" target="_blank" style="text-decoration: none;">
             <img src="${thumb}" alt="${esc(block.content.alt || 'Watch the video')}" width="600" style="display: block; width: 100%; max-width: 100%; height: auto; border-radius: ${esc(block.style.borderRadius || '8px')};" />
           </a>`
        : '';
      html = `
        <table role="presentation" border="0" cellpadding="0" cellspacing="0" width="100%" style="margin: 20px 0; ${styles}">
          <tr><td align="${align}">${thumbHtml}</td></tr>
          <tr>
            <td align="${align}" style="padding-top: 12px;">
              <a href="${href}" target="_blank" style="display: inline-block; background-color: #000000; color: #ffffff; font-family: sans-serif; font-size: 14px; line-height: 36px; mso-line-height-rule: exactly; padding: 0 20px; border-radius: 18px; text-decoration: none;">&#9654;&nbsp; Watch video</a>
            </td>
          </tr>
        </table>
      `;
      break;
    }

    case 'table': {
      const withBorder = block.content.withTableBorder !== false;
      const withColBorders = block.content.withColumnBorders !== false;
      const borderStyle = withBorder ? 'border: 1px solid #ddd;' : '';
      const cellBorderStyle = withColBorders
        ? 'border: 1px solid #ddd;'
        : withBorder
          ? 'border-bottom: 1px solid #ddd;'
          : '';

      const headerCells = (block.content.headers || [])
        .map((h: string) => `<th style="${cellBorderStyle} padding: 12px; background-color: #f8f9fa; text-align: left;">${esc(h)}</th>`)
        .join('');
      // An empty thead is invalid and confuses screen readers into announcing a
      // header row that is not there.
      const thead = headerCells ? `<thead><tr>${headerCells}</tr></thead>` : '';

      let rowsHtml = '';
      if (block.content.loopVariable) {
        const templateRow = (block.content.rows?.[0] || [])
          .map((cell: string) => `<td style="${cellBorderStyle} padding: 12px;">${esc(cell)}</td>`)
          .join('');
        rowsHtml = `
            {{#each ${esc(block.content.loopVariable)}}}
              <tr>${templateRow}</tr>
            {{/each}}
          `;
      } else {
        rowsHtml = (block.content.rows || [])
          .map(
            (row: string[]) => `
            <tr>
              ${(row || []).map((cell: string) => `<td style="${cellBorderStyle} padding: 12px;">${esc(cell)}</td>`).join('')}
            </tr>
          `,
          )
          .join('');
      }

      // A data table keeps its default semantics — role="presentation" belongs
      // only on the tables used for layout.
      //
      // Columns of data cannot meaningfully stack, so on a phone the wrapper
      // scrolls horizontally rather than stretching the whole message past the
      // viewport, which is what drags every other block sideways with it.
      html = `
        <div class="data-table-wrap" style="margin: 20px 0;">
          <table border="0" cellpadding="0" cellspacing="0" style="width: 100%; border-collapse: collapse; ${borderStyle} ${styles}">
            ${thead}
            <tbody>${rowsHtml}</tbody>
          </table>
        </div>
      `;
      break;
    }

    case 'columns': {
      const stackClass = block.content.stackOnMobile !== false ? 'stack-column' : '';
      const cols = (block.content.columns || [])
        .map((col: any) => {
          const colContent = (col?.blocks || [])
            .map((b: Block) => renderBlockToHTML(b, depth + 1, containerWidth))
            .join('\n');
          return `
          <td valign="top" width="${esc(col?.width || '50%')}" class="${stackClass}" style="padding: 10px; ${styleToString(col?.style)}">
            ${colContent}
          </td>
        `;
        })
        .join('');
      const inner = `
        <table role="presentation" border="0" cellpadding="0" cellspacing="0" width="100%" style="margin: 10px 0; ${styles}">
          <tr>
            ${cols}
          </tr>
        </table>
      `;

      html = withBackgroundImage(inner, block, containerWidth);
      break;
    }

    default:
      html = '';
  }

  // Repeating an arbitrary block, which is what a cart, a product grid or a
  // digest needs. Looping already existed but only inside `list` and `table`,
  // so a line item could be a row of text and nothing else — you could not
  // repeat an image beside a name beside a price, which is what those layouts
  // actually are.
  //
  // Wrapping here rather than adding a `repeat` block with its own children:
  // the builder recurses into `columns` at six separate sites (delete,
  // duplicate, re-id, update, reorder, render), and a second nesting tree would
  // have to be threaded through every one of them. A columns block that repeats
  // is the same capability, reuses machinery that is already tested, and adds
  // no new way for a nested block to become unreachable.
  //
  // `list` and `table` build their own each-block internally, so wrapping them
  // again would iterate twice.
  if (block.content.loopVariable && !SELF_LOOPING.has(block.type)) {
    html = wrapInEach(html, String(block.content.loopVariable), block.content.emptyText);
  }

  if (block.content.ifVariable) {
    return `{{#if ${esc(block.content.ifVariable)}}}\n${html}\n{{/if}}`;
  }

  return html;
};

// Blocks that already emit their own {{#each}} from inside their case.
const SELF_LOOPING = new Set<Block['type']>(['list', 'table']);

/**
 * Wraps rendered HTML in an each, with an optional empty state.
 *
 * The empty branch matters more than it looks. Without it a cart with nothing
 * in it renders as a heading, a total of zero and a gap where the items were,
 * which reads as a broken email rather than an empty one — and it is invisible
 * while authoring, because the author always has test data.
 */
export const wrapInEach = (html: string, variable: string, emptyText?: unknown): string => {
  const each = esc(variable.trim());
  if (!each) return html;

  const empty = String(emptyText ?? '').trim();
  if (!empty) {
    return `{{#each ${each}}}\n${html}\n{{/each}}`;
  }

  return `{{#each ${each}}}\n${html}\n{{else}}\n<p style="margin: 0; padding: 12px 0; color: #6b7280; font-family: sans-serif; font-size: 14px; mso-line-height-rule: exactly;">${esc(empty)}</p>\n{{/each}}`;
};

/**
 * The pixel height a background area is drawn at in Outlook.
 *
 * VML cannot size itself to its content — it needs a box before it knows what
 * goes in it — so a hero has to declare how tall it is. Every other client
 * ignores this and grows to fit, which means a mismatch shows up as Outlook
 * clipping or padding the area rather than as a broken layout.
 */
const DEFAULT_BACKGROUND_HEIGHT = 300;

/**
 * Wraps content in a background image that Outlook will also draw.
 *
 * Three mechanisms, because no single one works everywhere:
 *
 *   - `background-image` in CSS, for every modern client.
 *   - the `background` attribute on the cell, for older Outlook and some
 *     webmail that strips the CSS property but honours the attribute.
 *   - a VML rect, because Word's engine ignores both of the above. It is
 *     wrapped in a conditional comment so nothing else ever sees it.
 *
 * A background colour is always emitted alongside. Images are blocked by
 * default in most clients, so the colour is what most recipients actually see
 * on first open — a hero whose text is white on an unset background is
 * invisible until someone clicks "show images", which is a real way for a
 * message to arrive blank.
 */
export const withBackgroundImage = (inner: string, block: Block, containerWidth: number): string => {
  const src = safeImageUrl(block.content.backgroundImage);
  if (!src) return inner;

  const height = Number(block.content.backgroundHeight) || DEFAULT_BACKGROUND_HEIGHT;
  const color = String(block.style?.backgroundColor || block.content.backgroundColor || '#333333');
  const width = containerWidth > 0 ? containerWidth : 600;

  return `
    <table role="presentation" border="0" cellpadding="0" cellspacing="0" width="100%" style="mso-table-lspace:0pt;mso-table-rspace:0pt;border-collapse:collapse;">
      <tr>
        <td background="${src}" bgcolor="${esc(color)}" valign="top" style="background-image: url('${src}'); background-position: center center; background-size: cover; background-repeat: no-repeat; background-color: ${esc(color)};">
          <!--[if gte mso 9]>
          <v:rect xmlns:v="urn:schemas-microsoft-com:vml" fill="true" stroke="false" style="width:${width}px;height:${height}px;">
            <v:fill type="frame" src="${src}" color="${esc(color)}" />
            <v:textbox inset="0,0,0,0">
          <![endif]-->
          <div>
            ${inner}
          </div>
          <!--[if gte mso 9]>
            </v:textbox>
          </v:rect>
          <![endif]-->
        </td>
      </tr>
    </table>
  `;
};
