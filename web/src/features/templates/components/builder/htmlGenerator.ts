import { EmailDesign, Block } from './types';

const styleToString = (style: any) => {
  return Object.entries(style)
    .map(([prop, val]) => `${prop.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`)}: ${val}`)
    .join('; ');
};

export const generateHTML = (design: EmailDesign): string => {
  const { blocks, bodyStyle, preheader } = design;

  const bodyStyles = styleToString({
    margin: 0,
    padding: 0,
    width: '100%',
    backgroundColor: bodyStyle.backgroundColor || '#f4f4f4',
    fontFamily: bodyStyle.fontFamily || 'Inter, Helvetica, Arial, sans-serif',
    ...bodyStyle
  });

  const content = blocks.map(renderBlockToHTML).join('\n');

  const contentWidth = bodyStyle.contentWidth || '600px';
  const widthNumeric = contentWidth.replace(/[^0-9]/g, '');

  const preheaderHtml = preheader ? `
    <div style="display: none; max-height: 0px; overflow: hidden;">
      ${preheader}
    </div>
    <!-- Insert &zwnj;&nbsp; hack after hidden preview text -->
    <div style="display: none; max-height: 0px; overflow: hidden;">
      &zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;
    </div>
  ` : '';

  return `
<!DOCTYPE html>
<html xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="X-UA-Compatible" content="IE=edge">
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
    img { border: 0; height: auto; line-height: 100%; outline: none; text-decoration: none; display: block; max-width: 100%; }
    table { border-collapse: collapse !important; }
    body { height: 100% !important; margin: 0 !important; padding: 0 !important; width: 100% !important; }
    div[style*="margin: 16px 0;"] { margin: 0 !important; }
    @media screen and (max-width: ${widthNumeric}px) {
      .email-container { width: 100% !important; margin: auto !important; }
      .stack-column { display: block !important; width: 100% !important; max-width: 100% !important; direction: ltr !important; }
      .mobile-padding { padding-left: 10px !important; padding-right: 10px !important; }
    }
  </style>
</head>
<body style="${bodyStyles}">
  ${preheaderHtml}
  <table border="0" cellpadding="0" cellspacing="0" width="100%" style="mso-table-lspace:0pt;mso-table-rspace:0pt;border-collapse:collapse;">
    <tr>
      <td align="center" style="padding: 20px 0;">
        <!--[if mso]>
        <table align="center" border="0" cellspacing="0" cellpadding="0" width="${widthNumeric}">
        <tr>
        <td align="center" valign="top" width="${widthNumeric}">
        <![endif]-->
        <table border="0" cellpadding="0" cellspacing="0" width="${widthNumeric}" class="email-container" style="background-color: #ffffff; border-radius: 8px; overflow: hidden; box-shadow: 0 4px 6px rgba(0,0,0,0.05); mso-table-lspace:0pt;mso-table-rspace:0pt;border-collapse:collapse;">
          <tr>
            <td class="mobile-padding" style="padding: 40px 30px;">
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

const renderBlockToHTML = (block: Block): string => {
  const styles = styleToString(block.style);
  let html = '';

  switch (block.type) {
    case 'heading':
      const level = block.content.level || 'h1';
      html = `<${level} style="${styles}">${block.content.text}</${level}>`;
      break;
    case 'text':
      html = `<div style="${styles}">${block.content.text}</div>`;
      break;
    case 'button':
      // Outlook-friendly button using VML and tables
      const btnColor = block.style.color || '#ffffff';
      const btnBg = block.style.backgroundColor || '#0073ea';
      const btnRadius = block.style.borderRadius || '4px';
      const btnAlign = block.style.textAlign || 'center';
      const btnWidth = block.style.width || 'auto';
      const btnWidthMso = btnWidth === '100%' ? '500px' : '200px';

      html = `
        <table border="0" cellpadding="0" cellspacing="0" style="margin: 20px 0; width: ${btnWidth}; border-collapse: separate !important;">
          <tr>
            <td align="${btnAlign}">
              <div>
                <!--[if mso]>
                <v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="${block.content.url || '#'}" style="height:45px;v-text-anchor:middle;width:${btnWidthMso};" arcsize="${parseInt(String(btnRadius)) * 2}%" stroke="f" fillcolor="${btnBg}">
                  <w:anchorlock/>
                  <center style="color:${btnColor};font-family:sans-serif;font-size:${block.style.fontSize || '16px'};font-weight:${block.style.fontWeight || 'bold'};">${block.content.label}</center>
                </v:roundrect>
                <![endif]-->
                <a href="${block.content.url || '#'}" target="_blank" style="background-color:${btnBg};border-radius:${btnRadius};color:${btnColor};display:inline-block;font-family:sans-serif;font-size:${block.style.fontSize || '16px'};font-weight:${block.style.fontWeight || 'bold'};line-height:45px;text-align:center;text-decoration:none;width:${btnWidth};padding: 0 24px;-webkit-text-size-adjust:none;mso-hide:all;">
                  ${block.content.label}
                </a>
              </div>
            </td>
          </tr>
        </table>
      `;
      break;
    case 'image':
      const imgWidth = parseInt(String(block.style.width)) || 600;
      const imgHtml = `<img src="${block.content.src}" alt="${block.content.alt || ''}" width="${imgWidth}" style="display: block; width: ${block.style.width || '100%'}; max-width: 100%; height: auto; border-radius: ${block.style.borderRadius || '4px'}; ${styles}" />`;
      const imgContent = block.content.linkUrl
        ? `<a href="${block.content.linkUrl}" target="_blank">${imgHtml}</a>`
        : imgHtml;
      html = `
        <div style="margin: 20px 0; text-align: ${block.style.textAlign || 'center'};">
          <div style="display: inline-block; width: ${block.style.width || '100%'};">
            ${imgContent}
          </div>
        </div>
      `;
      break;
    case 'divider':
      const count = block.content.count || 1;
      const spacing = block.content.spacing || 5;
      const hrStyle = `border: 0; border-top: ${block.style.borderTopWidth || '1px'} ${block.style.borderTopStyle || 'solid'} ${block.style.borderTopColor || '#eeeeee'}; margin: 0;`;

      let dividers = '';
      for (let i = 0; i < count; i++) {
        dividers += `<hr style="${hrStyle} ${i > 0 ? `margin-top: ${spacing}px;` : ''}" />`;
      }
      html = `<div style="margin: 30px 0; ${styles}">${dividers}</div>`;
      break;
    case 'spacer':
      html = `<div style="height: ${block.content.height || '20px'}; ${styles}"></div>`;
      break;
    case 'list':
      if (block.content.loopVariable) {
          const itemAlias = block.content.loopItemVariable || 'item';
          const template = block.content.items?.[0] || `{{${itemAlias}}}`;
          html = `
          <ul style="${styles}">
            {{#each ${block.content.loopVariable}}}
              <li>${template}</li>
            {{/each}}
          </ul>
        `;
      } else {
        const itemsList = (block.content.items || []).map((item: string) => `<li>${item}</li>`).join('');
        html = `<ul style="${styles}">${itemsList}</ul>`;
      }
      break;
    case 'social':
      const icons = (block.content.links || []).map((link: any) => `
        <td style="padding: 0 5px;">
          <a href="${link.url}" target="_blank">
            <img src="${link.icon}" width="32" height="32" style="display: block; border: 0;" />
          </a>
        </td>
      `).join('');
      html = `
        <table border="0" cellpadding="0" cellspacing="0" align="${block.style.textAlign || 'center'}">
          <tr>${icons}</tr>
        </table>
      `;
      break;
    case 'video':
      html = `
        <div style="margin: 20px 0; text-align: ${block.style.textAlign || 'center'};">
          <a href="${block.content.url}" target="_blank" style="display: inline-block; position: relative; text-decoration: none;">
            <img src="${block.content.thumbnail || 'https://via.placeholder.com/600x340?text=Video+Placeholder'}" width="100%" style="display: block; border-radius: ${block.style.borderRadius || '8px'};" />
            <div style="position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); background: rgba(0,0,0,0.6); border-radius: 50%; width: 64px; height: 64px; display: flex; align-items: center; justify-content: center;">
               <div style="border-top: 15px solid transparent; border-bottom: 15px solid transparent; border-left: 25px solid white; margin-left: 5px;"></div>
            </div>
          </a>
        </div>
      `;
      break;
    case 'table':
      const withBorder = block.content.withTableBorder !== false;
      const withColBorders = block.content.withColumnBorders !== false;
      const borderStyle = withBorder ? 'border: 1px solid #ddd;' : '';
      const cellBorderStyle = withColBorders ? 'border: 1px solid #ddd;' : (withBorder ? 'border-bottom: 1px solid #ddd;' : '');

      const headers = (block.content.headers || []).map((h: string) => `<th style="${cellBorderStyle} padding: 12px; background-color: #f8f9fa; text-align: left;">${h}</th>`).join('');

      let rowsHtml = '';
      if (block.content.loopVariable) {
          const templateRow = (block.content.rows?.[0] || []).map((cell: string) => `<td style="${cellBorderStyle} padding: 12px;">${cell}</td>`).join('');
          rowsHtml = `
            {{#each ${block.content.loopVariable}}}
              <tr>${templateRow}</tr>
            {{/each}}
          `;
      } else {
          rowsHtml = (block.content.rows || []).map((row: string[]) => `
            <tr>
              ${row.map((cell: string) => `<td style="${cellBorderStyle} padding: 12px;">${cell}</td>`).join('')}
            </tr>
          `).join('');
      }

      html = `
        <table border="0" cellpadding="0" cellspacing="0" style="width: 100%; border-collapse: collapse; margin: 20px 0; ${borderStyle} ${styles}">
          <thead><tr>${headers}</tr></thead>
          <tbody>${rowsHtml}</tbody>
        </table>
      `;
      break;
    case 'columns':
      const stackClass = block.content.stackOnMobile !== false ? 'stack-column' : '';
      const cols = (block.content.columns || []).map((col: any) => {
        const colContent = (col.blocks || []).map(renderBlockToHTML).join('\n');
        return `
          <td valign="top" width="${col.width || '50%'}" class="${stackClass}" style="padding: 10px; ${styleToString(col.style || {})}">
            ${colContent}
          </td>
        `;
      }).join('');
      html = `
        <table border="0" cellpadding="0" cellspacing="0" width="100%" style="margin: 10px 0; ${styles}">
          <tr>
            ${cols}
          </tr>
        </table>
      `;
      break;
    default:
      html = '';
  }

  if (block.content.ifVariable) {
    return `{{#if ${block.content.ifVariable}}}\n${html}\n{{/if}}`;
  }

  return html;
};
