import { templateClient as client } from '../../../services/client';

export const templateService = {
  createTemplate: (values: any) => client.createTemplate(values),
  getTemplate: (id: string) => client.getTemplate({ id }),
  listTemplates: async (pageSize?: number, pageToken?: string) => {
    const res = await client.listTemplates({ pageSize, pageToken });
    return res;
  },
  updateTemplate: (id: string, values: any) => client.updateTemplate({ id, ...values }),
  deleteTemplate: (id: string) => client.deleteTemplate({ id }),
};
