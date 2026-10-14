import { describe, expect, test } from 'bun:test';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import { addEntries, checkSender, parseEntry, pruneCovered, sendsMail, smtpHints } from './allowedSenders';

// These cases mirror internal/email_provider/senders/senders_test.go: the form
// and the gateway have to agree on what an entry means.
describe('parseEntry', () => {
  test.each([
    ['Billing@Acme.com', 'billing@acme.com', 'address'],
    ['*@Acme.com', '*@acme.com', 'domain'],
    ['@acme.com', '*@acme.com', 'domain'],
    ['acme.com', '*@acme.com', 'domain'],
    ['Acme Ops <ops@acme.dev>', 'ops@acme.dev', 'address'],
  ])('%s is %s', (raw, value, kind) => {
    expect(parseEntry(raw)).toMatchObject({ value, kind });
  });

  test('a bare domain says what it was turned into', () => {
    expect(parseEntry('acme.com')).toMatchObject({ note: expect.stringContaining('*@acme.com') });
  });

  test.each(['*', '*@', '*.acme.com', '*@*.acme.com', 'bill*@acme.com', 'billing@', 'billing@acme', 'bill ing@acme.com'])(
    '%s is refused',
    (raw) => {
      expect(parseEntry(raw)).toHaveProperty('error');
    },
  );

  test('blank input is nothing, not an error', () => {
    expect(parseEntry('   ')).toBeNull();
  });
});

describe('addEntries', () => {
  test('a pasted list adds each entry', () => {
    const r = addEntries([], 'a@x.io, b@x.io; *@y.io');
    expect(r.entries).toEqual(['a@x.io', 'b@x.io', '*@y.io']);
    expect(r.rejected).toBe('');
  });

  test('a bad entry stays in the box with the reason, and the good ones are added', () => {
    const r = addEntries([], 'ok@acme.com billing@');
    expect(r.entries).toEqual(['ok@acme.com']);
    expect(r.rejected).toBe('billing@');
    expect(r.feedback[0].tone).toBe('error');
  });

  test('a duplicate is pointed at rather than added again', () => {
    const r = addEntries(['ops@acme.com'], 'OPS@acme.com');
    expect(r.entries).toEqual(['ops@acme.com']);
    expect(r.highlight).toBe('ops@acme.com');
  });

  test('an address a domain entry already covers is skipped', () => {
    const r = addEntries(['*@acme.com'], 'ops@acme.com');
    expect(r.entries).toEqual(['*@acme.com']);
    expect(r.feedback[0].message).toContain('already covered by *@acme.com');
  });

  test('a domain entry offers to remove the addresses it now covers', () => {
    const r = addEntries(['ops@acme.com', 'billing@acme.com', 'x@other.io'], '*@acme.com');
    expect(r.entries).toContain('*@acme.com');
    expect(r.feedback.at(-1)?.pruneDomain).toBe('acme.com');
    expect(pruneCovered(r.entries, 'acme.com')).toEqual(['x@other.io', '*@acme.com']);
  });

  test('the list it was given is not changed', () => {
    const current = ['a@x.io'];
    addEntries(current, 'b@x.io');
    expect(current).toEqual(['a@x.io']);
  });
});

describe('checkSender', () => {
  const list = ['*@acme.com', 'billing@acme-pay.io'];

  test('an empty list admits anyone', () => {
    expect(checkSender([], 'a@b.io')).toEqual({ allowed: true, by: null });
  });
  test('names the entry that admits the address', () => {
    expect(checkSender(list, 'Acme <Ops@Acme.com>')).toEqual({ allowed: true, by: '*@acme.com' });
    expect(checkSender(list, 'billing@acme-pay.io')).toEqual({ allowed: true, by: 'billing@acme-pay.io' });
  });
  test('a subdomain, another address or plus addressing is refused', () => {
    expect(checkSender(list, 'ops@mail.acme.com')).toEqual({ allowed: false });
    expect(checkSender(list, 'receipts@acme-pay.io')).toEqual({ allowed: false });
    expect(checkSender(list, 'billing+eu@acme-pay.io')).toEqual({ allowed: false });
  });
  test('half an address has no answer yet', () => {
    expect(checkSender(list, 'billing@')).toBeNull();
  });
});

test('IMAP and POP3 have no From to restrict', () => {
  expect(sendsMail(ProviderType.IMAP)).toBe(false);
  expect(sendsMail(ProviderType.POP3)).toBe(false);
  expect(sendsMail(ProviderType.SMTP)).toBe(true);
  expect(sendsMail(ProviderType.SES)).toBe(true);
});

describe('smtpHints', () => {
  test('suggests the signed-in account as the first entry', () => {
    expect(smtpHints([], 'notifications@acme.com', '')).toEqual([
      expect.objectContaining({ tone: 'info', add: 'notifications@acme.com' }),
    ]);
  });

  test('says when the account is covered', () => {
    expect(smtpHints(['*@acme.com'], 'notifications@acme.com', '')[0]).toMatchObject({ tone: 'success' });
  });

  test('warns when the list leaves the account out', () => {
    expect(smtpHints(['billing@acme.com'], 'notifications@acme.com', '')[0]).toMatchObject({
      tone: 'warning',
      add: 'notifications@acme.com',
    });
  });

  test('a username that is not an address gets no account hint', () => {
    expect(smtpHints([], 'apikey', '')).toEqual([]);
  });

  test('names entries that will not align with the DKIM domain', () => {
    const hints = smtpHints(['*@acme.com', 'billing@acme-pay.io', 'ops@mail.acme.com'], '', 'acme.com');
    expect(hints).toHaveLength(1);
    expect(hints[0].message).toContain('billing@acme-pay.io');
    expect(hints[0].message).not.toContain('ops@mail.acme.com');
  });
});
