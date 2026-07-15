import { providerClient, emailClient } from '../../../services/client';
import { 
  CreateEmailProviderRequest, 
  UpdateEmailProviderRequest,
} from '../../../api/panmail/v1/email_provider_service_pb';
import { 
  SmtpConfig, 
  ImapConfig, 
  Pop3Config 
} from '../../../api/panmail/v1/email_provider_pb';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import { SendEmailRequest } from '../../../api/panmail/v1/email_service_pb';

export const emailProviderService = {
  async listProviders(pageSize?: number, pageToken?: string) {
    const res = await providerClient.listEmailProviders({ pageSize, pageToken });
    return res;
  },

  async createProvider(values: any) {
    const req = new CreateEmailProviderRequest({
      name: values.name,
      type: values.type,
      config: values.type === ProviderType.SMTP ? { case: 'smtp', value: new SmtpConfig(values.smtp) } :
              values.type === ProviderType.IMAP ? { case: 'imap', value: new ImapConfig(values.imap) } :
              values.type === ProviderType.POP3 ? { case: 'pop3', value: new Pop3Config(values.pop3) } :
              undefined
    });

    const res = await providerClient.createEmailProvider(req);
    return res.provider;
  },

  async updateProvider(id: string, values: any) {
    const req = new UpdateEmailProviderRequest({
      id,
      name: values.name,
      config: values.type === ProviderType.SMTP ? { case: 'smtp', value: new SmtpConfig(values.smtp) } :
              values.type === ProviderType.IMAP ? { case: 'imap', value: new ImapConfig(values.imap) } :
              values.type === ProviderType.POP3 ? { case: 'pop3', value: new Pop3Config(values.pop3) } :
              undefined
    });

    const res = await providerClient.updateEmailProvider(req);
    return res.provider;
  },

  async deleteProvider(id: string) {
    await providerClient.deleteEmailProvider({ id });
  },

  async testProvider(id: string) {
    return await providerClient.testEmailProvider({ id });
  },

  async testProviderConfig(values: any) {
    const req = new CreateEmailProviderRequest({
      name: values.name,
      type: values.type,
      config: values.type === ProviderType.SMTP ? { case: 'smtp', value: new SmtpConfig(values.smtp) } :
              values.type === ProviderType.IMAP ? { case: 'imap', value: new ImapConfig(values.imap) } :
              values.type === ProviderType.POP3 ? { case: 'pop3', value: new Pop3Config(values.pop3) } :
              undefined
    });

    return await providerClient.testEmailProviderConfig(req);
  },

  async sendEmail(values: any) {
    const req = new SendEmailRequest(values);
    return await emailClient.sendEmail(req);
  }
};
