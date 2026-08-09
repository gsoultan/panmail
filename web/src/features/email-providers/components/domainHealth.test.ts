import { describe, expect, test } from 'bun:test';
import { DkimKeyMatch } from '../../../api/panmail/v1/email_provider_service_pb';
import { summarise } from './domainHealth';

const healthy = {
  domain: 'example.com',
  spf: { found: true, valid: true, record: 'v=spf1 ~all' },
  dmarc: { found: true, valid: true, record: 'v=DMARC1; p=reject' },
  mx: { found: true, valid: true, record: 'mx1.example.com (10)' },
  dkim: [
    {
      selector: 's1',
      dns: { found: true, valid: true, record: 'v=DKIM1; k=rsa; p=AAAA' },
      keyMatch: DkimKeyMatch.MATCHES,
    },
  ],
};

const rowFor = (health: Parameters<typeof summarise>[0], label: string) => {
  const row = summarise(health).rows.find((r) => r.label.startsWith(label));
  if (!row) throw new Error(`no row for ${label}`);
  return row;
};

describe('overall verdict', () => {
  test('a fully configured domain is healthy', () => {
    expect(summarise(healthy).overall).toBe('pass');
  });

  test('the worst row decides the verdict', () => {
    const result = summarise({ ...healthy, spf: { found: false } });
    expect(result.overall).toBe('fail');
  });

  test('a warning does not become a failure', () => {
    const result = summarise({ ...healthy, dmarc: { found: false } });
    expect(result.overall).toBe('warn');
  });
});

describe('DKIM', () => {
  // The case the whole feature exists for. DNS is well formed, the record
  // parses, and every message still fails verification.
  test('a key from a different pair is a failure, not a warning', () => {
    const row = rowFor(
      {
        ...healthy,
        dkim: [{
          selector: 's1',
          dns: { found: true, valid: true, record: 'v=DKIM1; k=rsa; p=BBBB' },
          keyMatch: DkimKeyMatch.MISMATCH,
          keyMatchDetails: 'belongs to a different key pair',
        }],
      },
      'DKIM',
    );

    expect(row.status).toBe('fail');
    expect(row.detail).toContain('different key pair');
  });

  test('a mismatch outranks a record that merely failed to parse', () => {
    // Both conditions at once: the mismatch is the one worth reporting,
    // because a parse complaint would send someone to fix the wrong thing.
    const row = rowFor(
      {
        ...healthy,
        dkim: [{
          selector: 's1',
          dns: { found: true, valid: false, record: 'v=DKIM1; p=BBBB', details: 'did not parse' },
          keyMatch: DkimKeyMatch.MISMATCH,
          keyMatchDetails: 'belongs to a different key pair',
        }],
      },
      'DKIM',
    );

    expect(row.detail).toContain('different key pair');
  });

  test('a matching key passes and says so', () => {
    const row = rowFor(healthy, 'DKIM');
    expect(row.status).toBe('pass');
    expect(row.detail).toContain('signs with');
  });

  test('an unpublished selector is a failure and names what is missing', () => {
    const row = rowFor(
      { ...healthy, dkim: [{ selector: 's1', dns: { found: false } }] },
      'DKIM',
    );
    expect(row.status).toBe('fail');
    expect(row.detail).toContain('s1._domainkey');
  });

  // Before the provider is saved there is no key to compare against, and
  // implying a verdict either way would be a guess.
  test('an unchecked key is a warning that explains why', () => {
    const row = rowFor(
      {
        ...healthy,
        dkim: [{
          selector: 's1',
          dns: { found: true, valid: true, record: 'v=DKIM1; p=AAAA' },
          keyMatch: DkimKeyMatch.UNSPECIFIED,
        }],
      },
      'DKIM',
    );
    expect(row.status).toBe('warn');
    expect(row.detail).toContain('Save the provider');
  });

  test('a domain with no DKIM configured gets no DKIM row', () => {
    const rows = summarise({ ...healthy, dkim: [] }).rows;
    expect(rows.some((r) => r.label.startsWith('DKIM'))).toBe(false);
  });

  test('several selectors each get a row', () => {
    const rows = summarise({
      ...healthy,
      dkim: [
        { selector: 's1', dns: { found: true, valid: true }, keyMatch: DkimKeyMatch.MATCHES },
        { selector: 's2', dns: { found: false } },
      ],
    }).rows.filter((r) => r.label.startsWith('DKIM'));

    expect(rows).toHaveLength(2);
    expect(rows[0].status).toBe('pass');
    expect(rows[1].status).toBe('fail');
  });
});

describe('SPF, DMARC and MX are graded by what happens to the mail', () => {
  test('missing SPF fails: nothing authorises the sending host', () => {
    expect(rowFor({ ...healthy, spf: { found: false } }, 'SPF').status).toBe('fail');
  });

  // Mail still delivers without DMARC, so calling it a failure would cry wolf
  // next to a broken DKIM key.
  test('missing DMARC warns rather than fails', () => {
    expect(rowFor({ ...healthy, dmarc: { found: false } }, 'DMARC').status).toBe('warn');
  });

  test('missing MX warns and says a send-only domain is fine', () => {
    const row = rowFor({ ...healthy, mx: { found: false } }, 'MX');
    expect(row.status).toBe('warn');
    expect(row.detail).toContain('send-only');
  });
});

describe('a resolver that could not answer', () => {
  // "We could not tell" and "nothing is published" call for different actions,
  // so they must not render the same.
  test('a lookup failure is unknown, not a missing record', () => {
    const row = rowFor({ ...healthy, spf: { error: 'connection refused' } }, 'SPF');
    expect(row.status).toBe('unknown');
    expect(row.detail).toContain('connection refused');
  });

  test('an unknown does not drag the verdict down to failure', () => {
    expect(summarise({ ...healthy, spf: { error: 'timeout' } }).overall).toBe('unknown');
  });
});

describe('the published record is carried through', () => {
  test('so it can be compared against what was intended', () => {
    expect(rowFor(healthy, 'SPF').record).toBe('v=spf1 ~all');
    expect(rowFor(healthy, 'DKIM').record).toContain('p=AAAA');
  });
});
