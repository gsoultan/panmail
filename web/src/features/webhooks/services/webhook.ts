import type { MessageInitShape } from '@bufbuild/protobuf';
import { webhookClient as client } from '../../../services/client';
import type {
  CreateWebhookRequestSchema,
  UpdateWebhookRequestSchema,
} from '../../../api/panmail/v1/webhook_service_pb';
import { WebhookTriggerEvent } from '../../../api/panmail/v1/webhook_pb';

// MessageInitShape rather than Partial<Message>: in protobuf-es v2 a message
// type carries a required $typeName, so Partial<T> describes something no
// caller can construct without hand-writing that field.
export const webhookService = {
  createWebhook: (req: MessageInitShape<typeof CreateWebhookRequestSchema>) =>
    client.createWebhook(req),
  listWebhooks: (pageSize?: number, pageToken?: string) =>
    client.listWebhooks({ pageSize, pageToken }),
  updateWebhook: (req: MessageInitShape<typeof UpdateWebhookRequestSchema>) =>
    client.updateWebhook(req),
  deleteWebhook: (id: string) => client.deleteWebhook({ id }),
};

export { WebhookTriggerEvent };
