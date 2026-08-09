import { renderTemplatePreview, type PreviewData } from './renderPreview';

/**
 * Wraps a rendered template in the resets an email client would apply.
 *
 * A browser and a mail client do not agree on defaults — margins on paragraphs,
 * table border spacing, image display — so a preview that skips these shows a
 * layout no recipient will ever see. Lifted out of the form component
 * unchanged; the only difference is that the substitution step now takes
 * sample data.
 */
export const buildPreviewDocument = (html: string, data?: PreviewData): string => {
  if (!html) return '';

  let processedHtml = renderTemplatePreview(html, data);

    // Comprehensive CSS Reset for accurate email rendering in preview
    const cssReset = `
      <style type="text/css">
        /* Basic Resets */
        body, table, td, a { -webkit-text-size-adjust: 100%; -ms-text-size-adjust: 100%; }
        table, td { mso-table-lspace: 0pt; mso-table-rspace: 0pt; }
        img { -ms-interpolation-mode: bicubic; border: 0; height: auto; line-height: 100%; outline: none; text-decoration: none; display: block; max-width: 100%; }
        table { border-collapse: collapse !important; }
        
        /* Precision Layout */
        body { 
          height: 100% !important; 
          margin: 0 !important; 
          padding: 0 !important; 
          width: 100% !important; 
          -webkit-font-smoothing: antialiased; 
          -moz-osx-font-smoothing: grayscale; 
        }
        
        /* Fix for Gmail margin on divs */
        div[style*="margin: 16px 0;"] { margin: 0 !important; }
        
        /* Ensure responsive behavior */
        * { box-sizing: border-box; }
        
        /* Mobile Precision: Prevent auto-scaling of text and ensure fluid layout */
        @media only screen and (max-width: 480px) {
          body, table, td, p, a, li, blockquote {
            -webkit-text-size-adjust: none !important;
          }
          .full-width { width: 100% !important; height: auto !important; }
          .mobile-center { text-align: center !important; }
        }
        
        /* Outlook specific fixes for high DPI */
        @media screen and (min-width: 0\\0) {
          td { mso-line-height-rule: exactly; }
        }

        /* Custom scrollbar for a cleaner look */
        ::-webkit-scrollbar { width: 8px; }
        ::-webkit-scrollbar-track { background: transparent; }
        ::-webkit-scrollbar-thumb { background: rgba(0,0,0,0.1); border-radius: 4px; }
        ::-webkit-scrollbar-thumb:hover { background: rgba(0,0,0,0.2); }
      </style>
    `;

    const metaTags = `
      <meta name="viewport" content="width=device-width, initial-scale=1">
      <meta name="x-apple-disable-message-reformatting">
      <meta name="format-detection" content="telephone=no, date=no, address=no, email=no">
    `;

    // Inject Viewport and CSS Reset
    if (processedHtml.includes('<head>')) {
      // Inject inside head
      if (!processedHtml.includes('name="viewport"') && !processedHtml.includes("name='viewport'")) {
        processedHtml = processedHtml.replace('<head>', `<head>${metaTags}`);
      }
      processedHtml = processedHtml.replace('</head>', `${cssReset}</head>`);
    } else if (processedHtml.includes('<html')) {
      // Create head if missing
      processedHtml = processedHtml.replace(/<html[^>]*>/, `$&<head>${metaTags}${cssReset}</head>`);
    } else {
      // Fragment: wrap or prepend
      processedHtml = `${metaTags}${cssReset}${processedHtml}`;
    }
    
    return processedHtml;
};
