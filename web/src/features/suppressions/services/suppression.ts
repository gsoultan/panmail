import { suppressionClient as client } from '../../../services/client';
import {
  chunkEntries,
  totalise,
  type ImportTotals,
  type ParsedEntry,
} from '../suppressionImport';

export const suppressionService = {
  addSuppression: (values: any) => client.addSuppression(values),
  removeSuppression: (email: string) => client.removeSuppression({ email }),
  listSuppressions: async (pageSize = 50, pageToken = '') => {
    const res = await client.listSuppressions({ pageSize, pageToken });
    return res;
  },
  checkSuppression: (email: string) => client.checkSuppression({ email }),

  /**
   * Imports a list, splitting it into requests the server will accept.
   *
   * Sending it in pieces is safe because importing is idempotent: a chunk that
   * is retried adds nothing the first attempt already wrote. The chunks go one
   * at a time rather than in parallel, so a large file cannot open dozens of
   * concurrent writes against the same table.
   */
  importSuppressions: async (entries: ParsedEntry[]): Promise<ImportTotals> => {
    const results = [];
    for (const chunk of chunkEntries(entries)) {
      const res = await client.importSuppressions({ entries: chunk });
      results.push({
        imported: res.imported,
        alreadySuppressed: res.alreadySuppressed,
        invalid: res.invalid,
        invalidSamples: res.invalidSamples,
      });
    }
    return totalise(results);
  },
};
