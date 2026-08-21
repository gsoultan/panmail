import type { MessageInitShape } from '@bufbuild/protobuf';
import { providerClient, emailClient } from '../../../services/client';
import type { CreateEmailProviderRequestSchema } from '../../../api/panmail/v1/email_provider_service_pb';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';

/**
 * The config oneof, as the request wants it.
 *
 * Typed from the schema rather than with bare `as const` strings, so a case
 * name that does not exist on the oneof is a compile error. protobuf-es v2
 * takes plain init objects, which is why nothing is constructed here any more.
 */
type ProviderConfig = MessageInitShape<typeof CreateEmailProviderRequestSchema>['config'];

/**
 * Maps form values onto the request's config oneof.
 *
 * One function rather than a ternary chain repeated at each call site: the same
 * mapping was written out three times, so a provider type added to two of them
 * sent an undefined config from the third and stored an empty configuration
 * with no error. The backend had the identical duplication.
 */
const configFor = (values: any): ProviderConfig => {
  switch (values.type) {
    case ProviderType.SMTP:
      return { case: 'smtp', value: values.smtp ?? {} };
    case ProviderType.IMAP:
      return { case: 'imap', value: values.imap ?? {} };
    case ProviderType.POP3:
      return { case: 'pop3', value: values.pop3 ?? {} };
    case ProviderType.SENDGRID:
      return { case: 'sendgrid', value: values.sendgrid ?? {} };
    case ProviderType.SES:
      return { case: 'ses', value: values.ses ?? {} };
    case ProviderType.POSTMARK:
      return { case: 'postmark', value: values.postmark ?? {} };
    case ProviderType.MAILGUN:
      return { case: 'mailgun', value: values.mailgun ?? {} };
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
    const res = await providerClient.createEmailProvider({
      name: values.name,
      type: values.type,
      config: configFor(values),
    });
    return res.provider;
  },

  async updateProvider(id: string, values: any) {
    const res = await providerClient.updateEmailProvider({
      id,
      name: values.name,
      config: configFor(values),
    });
    return res.provider;
  },

  async deleteProvider(id: string) {
    await providerClient.deleteEmailProvider({ id });
  },

  async testProvider(id: string) {
    return await providerClient.testEmailProvider({ id });
  },

  async testProviderConfig(values: any) {
    return await providerClient.testEmailProviderConfig({
      name: values.name,
      type: values.type,
      config: configFor(values),
    });
  },

  /**
   * Reads the DNS a sending domain publishes: SPF, DMARC, MX and each DKIM
   * selector, plus whether the published key is the one being signed with.
   *
   * Takes a provider id when there is one, because only then can the key
   * comparison happen — the private key never leaves the server, so the check
   * has to run where it lives.
   */
  async checkDomainHealth(args: { providerId?: string; domain?: string; selectors?: string[] }) {
    return await providerClient.checkDomainHealth({
      providerId: args.providerId ?? '',
      domain: args.domain ?? '',
      selectors: args.selectors ?? [],
    });
  },

  async sendEmail(values: any) {
    return await emailClient.sendEmail(values);
  }
};
