import { providerClient, emailClient } from '../../../services/client';
import { 
  CreateEmailProviderRequest, 
  UpdateEmailProviderRequest,
} from '../../../api/panmail/v1/email_provider_service_pb';
import {
  SmtpConfig,
  ImapConfig,
  Pop3Config,
  SendGridConfig,
  SesConfig,
  PostmarkConfig,
  MailgunConfig,
} from '../../../api/panmail/v1/email_provider_pb';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import { SendEmailRequest } from '../../../api/panmail/v1/email_service_pb';

/**
 * Maps form values onto the request's config oneof.
 *
 * One function rather than a ternary chain repeated at each call site: the same
 * mapping was written out three times, so a provider type added to two of them
 * sent an undefined config from the third and stored an empty configuration
 * with no error. The backend had the identical duplication.
 */
const configFor = (values: any) => {
  switch (values.type) {
    case ProviderType.SMTP:
      return { case: 'smtp' as const, value: new SmtpConfig(values.smtp) };
    case ProviderType.IMAP:
      return { case: 'imap' as const, value: new ImapConfig(values.imap) };
    case ProviderType.POP3:
      return { case: 'pop3' as const, value: new Pop3Config(values.pop3) };
    case ProviderType.SENDGRID:
      return { case: 'sendgrid' as const, value: new SendGridConfig(values.sendgrid) };
    case ProviderType.SES:
      return { case: 'ses' as const, value: new SesConfig(values.ses) };
    case ProviderType.POSTMARK:
      return { case: 'postmark' as const, value: new PostmarkConfig(values.postmark) };
    case ProviderType.MAILGUN:
      return { case: 'mailgun' as const, value: new MailgunConfig(values.mailgun) };
    default:
      return undefined;
  }
};

export const emailProviderService = {
  async listProviders(pageSize?: number, pageToken?: string, name?: string, type?: ProviderType) {
    const res = await providerClient.listEmailProviders({ pageSize, pageToken, name, type });
    return res;
  },

  async createProvider(values: any) {
    const req = new CreateEmailProviderRequest({
      name: values.name,
      type: values.type,
  config: configFor(values)
    });

    const res = await providerClient.createEmailProvider(req);
    return res.provider;
  },

  async updateProvider(id: string, values: any) {
    const req = new UpdateEmailProviderRequest({
      id,
      name: values.name,
  config: configFor(values)
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
  config: configFor(values)
    });

    return await providerClient.testEmailProviderConfig(req);
  },

  async sendEmail(values: any) {
    const req = new SendEmailRequest(values);
    return await emailClient.sendEmail(req);
  }
};
