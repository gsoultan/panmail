import type { PreviewData } from './renderPreview';

// Its own module rather than beside SampleDataPanel: a file that exports a
// component and anything else cannot be hot-swapped by Fast Refresh, so every
// edit to the panel reloaded the whole module in development.

/** Parses the panel's text into data the renderer can use, or undefined. */
export const parseSampleData = (json: string): PreviewData | undefined => {
  if (!json.trim()) return undefined;
  try {
    const data = JSON.parse(json);
    if (data && typeof data === 'object' && !Array.isArray(data)) return data as PreviewData;
  } catch {
    // An unparseable draft simply falls back to placeholders rather than
    // blanking the preview while it is being typed.
  }
  return undefined;
};
