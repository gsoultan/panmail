import { suppressionClient as client } from '../../../services/client';

export const suppressionService = {
  addSuppression: (values: any) => client.addSuppression(values),
  removeSuppression: (email: string) => client.removeSuppression({ email }),
  listSuppressions: async (pageSize = 50, pageToken = '') => {
    const res = await client.listSuppressions({ pageSize, pageToken });
    return res;
  },
  checkSuppression: (email: string) => client.checkSuppression({ email }),
};
