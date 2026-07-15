import { webhookClient as client } from '../../../services/client';
import { 
  CreateWebhookRequest, 
  UpdateWebhookRequest, 
  DeleteWebhookRequest, 
  ListWebhooksRequest 
} from '../../../api/panmail/v1/webhook_service_pb';
import { WebhookTriggerEvent } from '../../../api/panmail/v1/webhook_pb';

export const webhookService = {
  createWebhook: (req: Partial<CreateWebhookRequest>) => client.createWebhook(req),
  listWebhooks: (pageSize?: number, pageToken?: string) => client.listWebhooks({ pageSize, pageToken }),
  updateWebhook: (req: Partial<UpdateWebhookRequest>) => client.updateWebhook(req),
  deleteWebhook: (id: string) => client.deleteWebhook({ id }),
};

export { WebhookTriggerEvent };
