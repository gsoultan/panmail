import {
  SentCopyMode,
  type SentCopyMailbox,
  type SentCopySettings,
} from '../../../../api/panmail/v1/sent_copy_pb';
import type { SentCopyMailboxInit, SentCopySettingsInit } from '../../../../services/sentCopies';

/**
 * The editable state of a tenant's sent-copy settings.
 *
 * `masterPassword` is what the administrator has just typed, not what is
 * stored: the server never sends a password back, so an empty field means
 * "keep the stored one". `master_password_set` on the loaded settings is how
 * the form tells "kept" from "never set".
 */
export interface SentCopyFormValues {
  mode: SentCopyMode;
  imapHost: string;
  /** '' while the field is cleared, which saves as 0: the default port. */
  imapPort: number | '';
  imapUseSsl: boolean;
  sentFolder: string;
  masterUsername: string;
  masterPassword: string;
  masterSeparator: string;
}

export const IMPLICIT_TLS_PORT = 993;
export const STARTTLS_PORT = 143;
export const DEFAULT_SENT_FOLDER = 'Sent';
export const DEFAULT_SEPARATOR = '*';

const MAX_PORT = 65535;

/** UNSPECIFIED is what an unset field reads as, and the server treats it as NONE. */
export function normalizeMode(mode: SentCopyMode | undefined): SentCopyMode {
  // UNSPECIFIED is 0, so the falsy check covers it as well as a missing field.
  return mode ? mode : SentCopyMode.NONE;
}

/** Whether a mode files the copy over IMAP, and so needs the server fields. */
export function usesImap(mode: SentCopyMode): boolean {
  return mode === SentCopyMode.SHARED_LOGIN || mode === SentCopyMode.PER_USER_LOGIN;
}

/** The port the server uses when none is given. */
export function defaultImapPort(useSsl: boolean): number {
  return useSsl ? IMPLICIT_TLS_PORT : STARTTLS_PORT;
}

export function initialSentCopyValues(settings: SentCopySettings | null | undefined): SentCopyFormValues {
  return {
    mode: normalizeMode(settings?.mode),
    imapHost: settings?.imapHost ?? '',
    imapPort: settings?.imapPort ? settings.imapPort : '',
    // Implicit TLS for a tenant that has never configured a server: it is the
    // common case, and the stored false of an empty record is not a choice
    // anyone made.
    imapUseSsl: settings?.imapHost ? settings.imapUseSsl : true,
    sentFolder: settings?.sentFolder ?? '',
    masterUsername: settings?.masterUsername ?? '',
    masterPassword: '',
    masterSeparator: settings?.masterSeparator ?? '',
  };
}

export interface SentCopyContext {
  /** Whether the server already holds a master password. */
  masterPasswordSet: boolean;
  /** Whether the gateway can encrypt passwords; without it login modes are refused. */
  encryptionAvailable: boolean;
}

export type SentCopyErrors = Record<string, string | undefined>;

/**
 * Mirrors the server's rules so the panel can explain a refusal before it
 * happens. The server enforces them again; this only moves the explanation to
 * the field that caused it.
 */
export function validateSentCopy(values: SentCopyFormValues, context: SentCopyContext): SentCopyErrors {
  const errors: SentCopyErrors = {};
  if (!usesImap(values.mode)) return errors;

  if (!context.encryptionAvailable) {
    errors.mode = 'This gateway has no data key to encrypt passwords with, so this mode cannot be saved.';
  }
  if (!values.imapHost.trim()) {
    errors.imapHost = 'Enter the IMAP server copies are filed on.';
  }
  if (values.imapPort !== '') {
    const port = values.imapPort;
    if (!Number.isInteger(port) || port <= 0 || port > MAX_PORT) {
      errors.imapPort = 'Choose a port between 1 and 65535, or leave it empty for the default.';
    }
  }
  if (values.mode === SentCopyMode.SHARED_LOGIN) {
    if (!values.masterUsername.trim()) {
      errors.masterUsername = 'Enter the master user panmail logs in as.';
    }
    if (!values.masterPassword && !context.masterPasswordSet) {
      errors.masterPassword = 'Enter the master user’s password.';
    }
  }
  return errors;
}

export const hasErrors = (errors: SentCopyErrors): boolean => Object.values(errors).some(Boolean);

/**
 * The request body for UpdateSentCopySettings.
 *
 * The IMAP fields are sent whatever the mode, so switching to "None" and back
 * does not lose a configured server. The password is left out when blank:
 * blank means "keep the stored one", and omitting it makes that explicit.
 */
export function buildSentCopyUpdate(values: SentCopyFormValues): SentCopySettingsInit {
  const update: SentCopySettingsInit = {
    mode: normalizeMode(values.mode),
    imapHost: values.imapHost.trim(),
    imapPort: values.imapPort === '' ? 0 : values.imapPort,
    imapUseSsl: values.imapUseSsl,
    sentFolder: values.sentFolder.trim(),
    masterUsername: values.masterUsername.trim(),
    masterSeparator: values.masterSeparator.trim(),
  };
  // Not trimmed: a password is whatever was typed, spaces included.
  if (values.masterPassword) update.masterPassword = values.masterPassword;
  return update;
}

/** The editable state of one per-user mailbox login. */
export interface MailboxFormValues {
  address: string;
  username: string;
  password: string;
}

export function initialMailboxValues(mailbox: SentCopyMailbox | null | undefined): MailboxFormValues {
  return {
    address: mailbox?.address ?? '',
    username: mailbox?.username ?? '',
    password: '',
  };
}

export function validateMailbox(values: MailboxFormValues, passwordSet: boolean): SentCopyErrors {
  const errors: SentCopyErrors = {};
  const address = values.address.trim();
  if (!address) {
    errors.address = 'Enter the sender address.';
  } else if (!/^[^\s@]+@[^\s@]+$/.test(address)) {
    errors.address = 'Enter an email address, like alice@example.com.';
  }
  if (!values.password && !passwordSet) {
    errors.password = 'Enter the mailbox password.';
  }
  return errors;
}

/** The request body for SaveSentCopyMailbox; a blank password keeps the stored one. */
export function buildMailboxSave(values: MailboxFormValues): SentCopyMailboxInit {
  const mailbox: SentCopyMailboxInit = {
    address: values.address.trim(),
    username: values.username.trim(),
  };
  if (values.password) mailbox.password = values.password;
  return mailbox;
}
