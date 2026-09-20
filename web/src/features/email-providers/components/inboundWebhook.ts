import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';

/**
 * The inbound webhook an ESP posts delivery events to.
 *
 * This is the opposite direction from the Webhooks page: there, panmail posts
 * to the tenant. Here the provider posts to panmail, and what arrives is the
 * bounce, complaint and delivery record that keeps the suppression list
 * honest. Without it a send is one-way — mail goes out and nothing ever says
 * an address is dead.
 *
 * The handler routes on the last path segment, so the segment below is the
 * contract with `internal/event/transports/http/webhook_handler.go`, not a
 * display name. Getting it wrong falls through to the generic verifier and
 * every event is refused with a 401 that says only "verification failed".
 */

/** Providers that push events back, and the path segment each is routed by. */
const WEBHOOK_TYPES: Partial<Record<ProviderType, string>> = {
  [ProviderType.SENDGRID]: 'sendgrid',
  [ProviderType.MAILGUN]: 'mailgun',
  [ProviderType.POSTMARK]: 'postmark',
  [ProviderType.SES]: 'ses',
};

/**
 * The routing segment for a provider type, or null when the type has no
 * inbound events at all.
 *
 * SMTP, IMAP and POP3 return null deliberately: an SMTP send learns its
 * outcome from the conversation itself, and there is no third party to post
 * anything back. Offering a URL for them would invite an operator to configure
 * something that can never fire.
 */
export function webhookTypeFor(type: ProviderType): string | null {
  return WEBHOOK_TYPES[type] ?? null;
}

export interface WebhookUrlParts {
  baseUrl: string;
  tenantId: string;
  providerId: string;
  type: string;
}

/**
 * Builds the URL to paste into the provider's dashboard.
 *
 * The base is the configured public base URL rather than the address the
 * console happens to be open on: the dashboard is often reached over a
 * private hostname or a port-forward, and a provider has to reach this from
 * the internet. An empty base yields an empty string rather than a relative
 * path, because half a URL pasted into SendGrid fails silently.
 */
export function inboundWebhookUrl({ baseUrl, tenantId, providerId, type }: WebhookUrlParts): string {
  if (!baseUrl || !tenantId || !providerId || !type) return '';
  return `${baseUrl.replace(/\/+$/, '')}/webhooks/${tenantId}/${providerId}/${type}`;
}

export interface SecretField {
  label: string;
  description: string;
  placeholder: string;
}

/**
 * What the stored secret means, which is different for every provider.
 *
 * It is one column in the database and four different things on the wire, so
 * the field has to say which one it wants. An operator who pastes a SendGrid
 * verification key into an SES provider gets a 401 and no explanation.
 */
export function secretFieldFor(type: ProviderType): SecretField | null {
  switch (type) {
    case ProviderType.SENDGRID:
      return {
        label: 'Event webhook verification key',
        description:
          'The public key SendGrid shows when you enable signed event webhooks. Panmail checks every event against it.',
        placeholder: 'Base64 public key',
      };
    case ProviderType.MAILGUN:
      return {
        label: 'Webhook signing key',
        description:
          'From Mailgun’s API security settings — the HTTP webhook signing key, not the sending API key.',
        placeholder: 'Signing key',
      };
    case ProviderType.POSTMARK:
      return {
        label: 'Webhook credentials',
        description:
          'Postmark has no signature scheme, so it authenticates with HTTP basic credentials embedded in the webhook URL. Enter them as user:password and use the URL above with those credentials in it.',
        placeholder: 'user:password',
      };
    case ProviderType.SES:
      return {
        label: 'SNS topic ARN',
        description:
          'SES events arrive through SNS, which signs them, so there is no shared secret. Panmail still needs the topic ARN: a valid signature only proves a message came from SNS, not that it came from your topic.',
        placeholder: 'arn:aws:sns:us-east-1:123456789012:ses-events',
      };
    default:
      return null;
  }
}

/**
 * Whether the URL can be shown yet.
 *
 * It contains the provider's id, which does not exist until the provider is
 * saved. Showing a blank or half-built URL on the create form would be worse
 * than saying so.
 */
export function canShowWebhookUrl(providerId: string | undefined): providerId is string {
  return Boolean(providerId);
}
