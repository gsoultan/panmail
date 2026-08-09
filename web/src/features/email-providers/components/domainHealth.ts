import { DkimKeyMatch } from '../../../api/panmail/v1/email_provider_service_pb';

/**
 * Turns the raw DNS answers into rows an operator can act on.
 *
 * The severities are the opinionated part, and they are not uniform: a missing
 * DMARC record costs deliverability, whereas a DKIM key that does not match the
 * one being signed with breaks every message. Reporting both as "warning" would
 * bury the one that matters, so they are graded by what actually happens to the
 * mail rather than by how alarming the record looks.
 */

export type CheckStatus = 'pass' | 'warn' | 'fail' | 'unknown';

export interface CheckRow {
  label: string;
  status: CheckStatus;
  detail: string;
  record?: string;
}

export interface HealthSummary {
  domain: string;
  overall: CheckStatus;
  rows: CheckRow[];
}

/** Anything the server could not look up at all. */
const lookupFailed = (check?: { error?: string }) => Boolean(check?.error);

const SEVERITY: Record<CheckStatus, number> = { pass: 0, unknown: 1, warn: 2, fail: 3 };

const worst = (statuses: CheckStatus[]): CheckStatus =>
  statuses.reduce<CheckStatus>((acc, s) => (SEVERITY[s] > SEVERITY[acc] ? s : acc), 'pass');

interface RawCheck {
  found?: boolean;
  valid?: boolean;
  record?: string;
  details?: string;
  error?: string;
}

interface RawDkim {
  selector?: string;
  dns?: RawCheck;
  keyMatch?: DkimKeyMatch;
  keyMatchDetails?: string;
}

interface RawHealth {
  domain?: string;
  spf?: RawCheck;
  dmarc?: RawCheck;
  mx?: RawCheck;
  dkim?: RawDkim[];
}

const spfRow = (check?: RawCheck): CheckRow => {
  if (lookupFailed(check)) {
    return { label: 'SPF', status: 'unknown', detail: `Could not be looked up: ${check!.error}` };
  }
  if (!check?.found) {
    // Without SPF the receiving side has nothing authorising this host, and
    // DMARC cannot pass on DKIM alone unless the signature aligns.
    return {
      label: 'SPF',
      status: 'fail',
      detail: 'No SPF record. Recipients have nothing authorising this server to send for the domain.',
    };
  }
  if (!check.valid) {
    return { label: 'SPF', status: 'warn', detail: check.details || 'The SPF record did not parse cleanly.', record: check.record };
  }
  return { label: 'SPF', status: 'pass', detail: 'Published and parseable.', record: check.record };
};

const dmarcRow = (check?: RawCheck): CheckRow => {
  if (lookupFailed(check)) {
    return { label: 'DMARC', status: 'unknown', detail: `Could not be looked up: ${check!.error}` };
  }
  if (!check?.found) {
    // A warning rather than a failure: mail still delivers without DMARC, it
    // just gets less benefit of the doubt and the domain is spoofable.
    return {
      label: 'DMARC',
      status: 'warn',
      detail: 'No DMARC record. Mail still delivers, but the domain is spoofable and gets less trust.',
    };
  }
  if (!check.valid) {
    return { label: 'DMARC', status: 'warn', detail: check.details || 'The DMARC record did not parse cleanly.', record: check.record };
  }
  return { label: 'DMARC', status: 'pass', detail: 'Published and parseable.', record: check.record };
};

const mxRow = (check?: RawCheck): CheckRow => {
  if (lookupFailed(check)) {
    return { label: 'MX', status: 'unknown', detail: `Could not be looked up: ${check!.error}` };
  }
  if (!check?.found) {
    // Send-only domains legitimately have no MX. It still matters, because
    // bounces and replies have nowhere to go, so it is worth saying — but it
    // is not a reason to call the domain broken.
    return {
      label: 'MX',
      status: 'warn',
      detail: 'No MX record. Fine for a send-only domain, but bounces and replies have nowhere to go.',
    };
  }
  return { label: 'MX', status: 'pass', detail: 'Mail can be delivered back to this domain.', record: check.record };
};

const dkimRow = (dkim: RawDkim): CheckRow => {
  const label = `DKIM (${dkim.selector || '—'})`;
  const dns = dkim.dns;

  if (lookupFailed(dns)) {
    return { label, status: 'unknown', detail: `Could not be looked up: ${dns!.error}` };
  }

  if (!dns?.found) {
    return {
      label,
      status: 'fail',
      detail: `Nothing published at ${dkim.selector}._domainkey. Messages are signed with a key no verifier can find, which fails DMARC alignment.`,
    };
  }

  // The case the whole check exists for. A record that parses perfectly but
  // carries the wrong key is valid DNS and total delivery failure, so it
  // outranks a record that merely failed to parse.
  if (dkim.keyMatch === DkimKeyMatch.MISMATCH) {
    return {
      label,
      status: 'fail',
      detail: dkim.keyMatchDetails
        || 'The published key belongs to a different key pair, so every signature sent will fail verification.',
      record: dns.record,
    };
  }

  if (!dns.valid) {
    return { label, status: 'fail', detail: dns.details || 'The DKIM record did not parse cleanly.', record: dns.record };
  }

  if (dkim.keyMatch === DkimKeyMatch.MATCHES) {
    return { label, status: 'pass', detail: 'Published, and it is the key this provider signs with.', record: dns.record };
  }

  if (dkim.keyMatch === DkimKeyMatch.UNKNOWN) {
    return { label, status: 'warn', detail: dkim.keyMatchDetails || 'The published key could not be compared.', record: dns.record };
  }

  // UNSPECIFIED: no private key was available, which is the case when checking
  // a domain before the provider is saved. Saying so beats implying a verdict.
  return {
    label,
    status: 'warn',
    detail: 'Published. Save the provider to check it against the key being signed with.',
    record: dns.record,
  };
};

export const summarise = (health: RawHealth): HealthSummary => {
  const rows: CheckRow[] = [
    spfRow(health.spf),
    dmarcRow(health.dmarc),
    ...(health.dkim ?? []).map(dkimRow),
    mxRow(health.mx),
  ];

  return {
    domain: health.domain ?? '',
    overall: worst(rows.map((r) => r.status)),
    rows,
  };
};
