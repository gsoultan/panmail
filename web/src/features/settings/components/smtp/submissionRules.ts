import { SmtpBindScope, type SmtpSubmission } from '../../../../api/panmail/v1/system_settings_pb';

/**
 * The editable state of the submission listener.
 *
 * The two PEM fields are what the administrator has just pasted, not what is
 * stored: the server never sends a private key back, so an empty field means
 * "leave the stored pair alone" rather than "there is no certificate".
 * `hasStoredCertificate` is how the form tells those apart.
 */
export interface SmtpSubmissionFormValues {
  enabled: boolean;
  bindScope: SmtpBindScope;
  port: number;
  certificatePem: string;
  privateKeyPem: string;
  allowInsecureAuth: boolean;
  clearTls: boolean;
}

export const SUBMISSION_PORT = 587;

// The port a mail exchanger answers on. panmail authenticates every sender and
// is not an MX, so a listener here would collect delivery attempts it rejects.
const RELAY_PORT = 25;

const MAX_PORT = 65535;

export function initialFormValues(submission: SmtpSubmission | null | undefined): SmtpSubmissionFormValues {
  return {
    enabled: submission?.enabled ?? false,
    bindScope: submission?.bindScope || SmtpBindScope.LOOPBACK,
    port: submission?.port || SUBMISSION_PORT,
    certificatePem: '',
    privateKeyPem: '',
    allowInsecureAuth: submission?.insecureAuthAllowed ?? false,
    clearTls: false,
  };
}

/**
 * Whether a certificate will be in force once these values are saved.
 *
 * Three inputs decide it and all three matter: what is stored, what has been
 * pasted, and whether the stored pair is being removed.
 */
export function willHaveTls(values: SmtpSubmissionFormValues, hasStoredCertificate: boolean): boolean {
  if (values.certificatePem.trim() && values.privateKeyPem.trim()) return true;
  if (values.clearTls) return false;
  return hasStoredCertificate;
}

export interface FormProblem {
  field: keyof SmtpSubmissionFormValues | 'form';
  message: string;
}

/**
 * validateForm mirrors the server's rules so the panel can explain a refusal
 * before it happens.
 *
 * The server is still the authority — every one of these is enforced again in
 * internal/smtp_submission/entities, and it has to be, because this file runs
 * in a browser the caller controls. Duplicating them here buys an explanation
 * at the point of the mistake, nothing more.
 */
export function validateForm(
  values: SmtpSubmissionFormValues,
  hasStoredCertificate: boolean,
): FormProblem[] {
  const problems: FormProblem[] = [];

  if (!Number.isInteger(values.port) || values.port <= 0 || values.port > MAX_PORT) {
    problems.push({ field: 'port', message: 'Choose a port between 1 and 65535.' });
  } else if (values.port === RELAY_PORT) {
    problems.push({
      field: 'port',
      message: 'Port 25 is the mail exchanger port, not a submission port. Use 587.',
    });
  }

  const pastedCert = values.certificatePem.trim().length > 0;
  const pastedKey = values.privateKeyPem.trim().length > 0;

  if (pastedCert !== pastedKey) {
    problems.push({
      field: pastedCert ? 'privateKeyPem' : 'certificatePem',
      message: 'Paste the certificate and the private key together, or neither to keep the stored pair.',
    });
  }
  if (values.clearTls && (pastedCert || pastedKey)) {
    problems.push({
      field: 'form',
      message:
        'Removing the stored certificate and installing a new one are opposite instructions. Do one or the other.',
    });
  }

  const onLoopback = values.bindScope === SmtpBindScope.LOOPBACK;
  const willHave = willHaveTls(values, hasStoredCertificate);

  // The rule that matters most. The SMTP password is an API key, so accepting
  // sign-in without TLS anywhere it could cross a network hands out a tenant's
  // whole sending authority to anything on the path.
  if (values.allowInsecureAuth && !onLoopback) {
    problems.push({
      field: 'allowInsecureAuth',
      message:
        'Sign-in without TLS is only possible on a listener this machine alone can reach. The password is an API key.',
    });
  }
  if (!onLoopback && !willHave) {
    problems.push({
      field: 'certificatePem',
      message: 'A listener reachable from other machines needs a TLS certificate.',
    });
  }
  if (values.enabled && !willHave && !values.allowInsecureAuth) {
    problems.push({
      field: 'form',
      message:
        'Install a certificate, or allow sign-in without TLS on a loopback listener. Otherwise nothing can authenticate.',
    });
  }

  return problems;
}

/** The first problem attached to a field, for inline display. */
export function problemFor(problems: FormProblem[], field: FormProblem['field']): string | undefined {
  return problems.find((p) => p.field === field)?.message;
}
