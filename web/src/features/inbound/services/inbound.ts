import { inboundClient as client } from '../../../services/client';

export const inboundService = {
  listInboundEmails: async (pageSize = 50, pageToken = '') => {
    const res = await client.listInboundEmails({ pageSize, pageToken });
    return res;
  },
  getInboundEmail: (id: string) => client.getInboundEmail({ id }),
};
