import { describe, expect, test } from 'bun:test';
import { SentCopyMode, type SentCopySettings } from '../../../../api/panmail/v1/sent_copy_pb';
import {
  buildMailboxSave,
  buildSentCopyUpdate,
  initialSentCopyValues,
  normalizeMode,
  usesImap,
  validateMailbox,
  validateSentCopy,
  type SentCopyFormValues,
} from './sentCopyRules';

const values = (fields: Partial<SentCopyFormValues> = {}): SentCopyFormValues => ({
  mode: SentCopyMode.SHARED_LOGIN,
  imapHost: 'imap.example.com',
  imapPort: '',
  imapUseSsl: true,
  sentFolder: '',
  masterUsername: 'admin',
  masterPassword: '',
  masterSeparator: '',
  ...fields,
});

const ok = { masterPasswordSet: true, encryptionAvailable: true };

describe('sentCopyRules', () => {
  test('UNSPECIFIED reads as NONE', () => {
    expect(normalizeMode(SentCopyMode.UNSPECIFIED)).toBe(SentCopyMode.NONE);
    expect(initialSentCopyValues(null).mode).toBe(SentCopyMode.NONE);
  });

  test('only the login modes use IMAP', () => {
    expect(usesImap(SentCopyMode.SHARED_LOGIN)).toBe(true);
    expect(usesImap(SentCopyMode.PER_USER_LOGIN)).toBe(true);
    expect(usesImap(SentCopyMode.NONE)).toBe(false);
    expect(usesImap(SentCopyMode.BCC_SENDER)).toBe(false);
  });

  test('the stored password is never seeded into the form', () => {
    const seeded = initialSentCopyValues({
      mode: SentCopyMode.SHARED_LOGIN,
      imapHost: 'imap.example.com',
      imapPort: 143,
      imapUseSsl: false,
      masterPasswordSet: true,
    } as SentCopySettings);
    expect(seeded.masterPassword).toBe('');
    expect(seeded.imapPort).toBe(143);
    expect(seeded.imapUseSsl).toBe(false);
  });

  test('a blank password is left out of the update, so the stored one is kept', () => {
    const update = buildSentCopyUpdate(values());
    expect('masterPassword' in update).toBe(false);
    expect(update.imapPort).toBe(0);
  });

  test('a typed password is sent untrimmed', () => {
    expect(buildSentCopyUpdate(values({ masterPassword: ' s3cret ' })).masterPassword).toBe(' s3cret ');
  });

  test('a master password is required only when none is stored', () => {
    expect(validateSentCopy(values(), ok).masterPassword).toBeUndefined();
    expect(validateSentCopy(values(), { ...ok, masterPasswordSet: false }).masterPassword).toBeTruthy();
  });

  test('login modes need a host and encryption', () => {
    const errors = validateSentCopy(values({ imapHost: ' ' }), { ...ok, encryptionAvailable: false });
    expect(errors.imapHost).toBeTruthy();
    expect(errors.mode).toBeTruthy();
  });

  test('NONE and BCC need nothing', () => {
    const none = validateSentCopy(values({ mode: SentCopyMode.NONE, imapHost: '' }), {
      masterPasswordSet: false,
      encryptionAvailable: false,
    });
    expect(Object.values(none).filter(Boolean)).toHaveLength(0);
  });

  test('an out-of-range port is refused', () => {
    expect(validateSentCopy(values({ imapPort: 70000 }), ok).imapPort).toBeTruthy();
    expect(validateSentCopy(values({ imapPort: 993 }), ok).imapPort).toBeUndefined();
  });

  test('mailbox: new rows need a password, stored rows keep theirs', () => {
    const row = { address: 'alice@example.com', username: '', password: '' };
    expect(validateMailbox(row, false).password).toBeTruthy();
    expect(validateMailbox(row, true).password).toBeUndefined();
    expect(validateMailbox({ ...row, address: 'nope' }, true).address).toBeTruthy();
    expect('password' in buildMailboxSave(row)).toBe(false);
    expect(buildMailboxSave({ ...row, password: 'pw' }).password).toBe('pw');
  });
});
