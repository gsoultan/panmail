import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';

/**
 * Turns the provider form's values into what the API should receive.
 *
 * Two of the SMTP fields exist only in the form. The server infers whether to
 * sign from whether the DKIM values are present, and whether to use OAuth from
 * whether the OAuth values are present — there is no "enabled" flag on the
 * wire. So the switches have to be stripped, and turning one off has to clear
 * the values behind it. Leaving them would keep the feature active while the
 * switch reads off, which is the kind of mismatch nobody notices until mail
 * stops being signed.
 *
 * This is a pure function rather than logic inside the submit handler because
 * it is the part that has been edited every time a provider feature landed —
 * DKIM, then OAuth — and each edit is a chance to drop one of the fields.
 */

export interface SmtpFormValues {
  dkimEnabled?: boolean;
  // Widened to string because the form's defaults are a plain object literal;
  // deriveSwitches still returns the narrow union.
  authMode?: string;
  dkim?: { domain?: string; selector?: string; privateKey?: string };
  oauth2?: Record<string, string>;
  [key: string]: unknown;
}

export interface ProviderFormValues {
  type: ProviderType;
  smtp?: SmtpFormValues;
  [key: string]: unknown;
}

const EMPTY_DKIM = { domain: '', selector: '', privateKey: '' };

const EMPTY_OAUTH = {
  mechanism: '',
  clientId: '',
  clientSecret: '',
  refreshToken: '',
  tokenEndpoint: '',
  scope: '',
};

export const toProviderRequest = (values: ProviderFormValues): ProviderFormValues => {
  // Only SMTP carries the form-only switches; the other types pass through.
  if (values.type !== ProviderType.SMTP || !values.smtp) return values;

  const { dkimEnabled, authMode, ...smtp } = values.smtp;

  const withDkim = dkimEnabled ? smtp : { ...smtp, dkim: { ...EMPTY_DKIM } };
  const withAuth =
    authMode === 'oauth2' ? withDkim : { ...withDkim, oauth2: { ...EMPTY_OAUTH } };

  return { ...values, smtp: withAuth };
};

/**
 * Derives the switch positions when loading a saved provider.
 *
 * Reads redact the secrets, so presence of a domain or a client id is the only
 * available signal that the feature was configured. Using the secret would show
 * the switch as off for every provider that has one.
 */
export const deriveSwitches = (smtp: SmtpFormValues | undefined): {
  dkimEnabled: boolean;
  authMode: 'password' | 'oauth2';
} => ({
  dkimEnabled: Boolean(smtp?.dkim?.domain || smtp?.dkim?.selector),
  authMode: smtp?.oauth2?.clientId || smtp?.oauth2?.tokenEndpoint ? 'oauth2' : 'password',
});

/**
 * Seeds the SMTP branch of the form from a provider loaded off the server.
 *
 * The switches have to be materialised here rather than defaulted at the point
 * of display. The sections do fall back sensibly when rendering — DkimSection
 * reads `dkimEnabled ?? Boolean(domain || selector)` — but that fallback only
 * reaches the checkbox, not the form state. Submit reads the state, finds the
 * switch undefined, treats it as off and clears the values behind it. The
 * effect was that opening a DKIM-signed provider, renaming it and saving
 * silently dropped its signing key, with the switch showing on throughout.
 */
export const seedSmtpValues = (smtp: SmtpFormValues | undefined, defaults: SmtpFormValues): SmtpFormValues => {
  const merged = { ...defaults, ...(smtp ?? {}) };
  return { ...merged, ...deriveSwitches(merged) };
};
