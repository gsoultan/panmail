import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';

/**
 * The provider's allowed senders: which From addresses it may send as.
 *
 * Each entry is an exact address (`billing@acme.com`) or a whole domain
 * (`*@acme.com`, which does not reach subdomains). An empty list means the
 * provider is not restricted.
 *
 * These rules mirror internal/email_provider/senders on the server, which
 * stays the authority: it repeats the validation on save and the match on
 * every send. Keeping the two in step is what makes the checker in the form
 * trustworthy, so a change to one belongs in the other.
 */

const WILDCARD = '*';

const DOMAIN_RE = /^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$/;
const LOCAL_RE = /^[a-z0-9.!#$%&'+/=?^_`{|}~-]{1,64}$/;

export type EntryKind = 'address' | 'domain';

export type ParsedEntry =
  | { value: string; kind: EntryKind; note?: string }
  | { error: string };

/** Reduces "Name <a@b>" to "a@b"; anything else is returned trimmed. */
export const stripDisplayName = (raw: string): string => {
  const m = raw.match(/<([^>]*)>\s*$/);
  return (m ? m[1] : raw).trim();
};

export const domainOf = (entry: string): string => entry.slice(entry.lastIndexOf('@') + 1);

export const isDomainEntry = (entry: string): boolean => entry.startsWith(`${WILDCARD}@`);

/**
 * Validates one entry as typed and returns it in canonical form, or the reason
 * it cannot be used. Returns null for blank input.
 */
export const parseEntry = (raw: string): ParsedEntry | null => {
  const typed = raw.trim();
  let s = stripDisplayName(typed).toLowerCase();
  if (!s) return null;

  if (s === WILDCARD || s === '*@' || s === '*@*') {
    return { error: 'A lone * would allow every address. Leave the list empty to allow any sender.' };
  }

  let note: string | undefined;
  if (s.startsWith('@')) {
    s = WILDCARD + s;
  } else if (!s.includes('@')) {
    if (s.includes(WILDCARD)) {
      return { error: `"${typed}" is not supported. Wildcards only work as *@domain, for example *@acme.com.` };
    }
    s = `${WILDCARD}@${s}`;
    note = `Added "${typed}" as ${s}, which allows any address at that domain.`;
  }

  const at = s.lastIndexOf('@');
  const local = s.slice(0, at);
  const domain = s.slice(at + 1);
  if (!local || !domain) {
    return { error: `"${typed}" is not a full address. Use name@domain, or *@domain for a whole domain.` };
  }
  if ((local !== WILDCARD && local.includes(WILDCARD)) || domain.includes(WILDCARD)) {
    return { error: `"${typed}" is not supported. Wildcards only work as *@domain, for example *@acme.com.` };
  }
  if (!DOMAIN_RE.test(domain)) {
    return { error: `"${domain}" is not a valid domain.` };
  }
  if (local === WILDCARD) {
    return { value: s, kind: 'domain', note };
  }
  if (!LOCAL_RE.test(local)) {
    return { error: `"${typed}" is not a valid email address.` };
  }
  return { value: s, kind: 'address', note };
};

export type Feedback = { tone: 'error' | 'warning' | 'info'; message: string; pruneDomain?: string };

export interface AddResult {
  entries: string[];
  /** Input that could not be added, left in the box to be fixed. */
  rejected: string;
  feedback: Feedback[];
  /** An existing entry the input duplicated, to point at. */
  highlight?: string;
}

/**
 * Adds typed or pasted input to a list. Input may hold several entries
 * separated by commas, semicolons or whitespace.
 *
 * Duplicates and addresses already covered by a *@domain entry are skipped
 * with a note rather than added, so the list never says the same thing twice.
 * A new *@domain entry that covers addresses already listed is added, and the
 * feedback offers to remove them.
 */
export const addEntries = (current: readonly string[], input: string): AddResult => {
  const entries = [...current];
  const rejected: string[] = [];
  const feedback: Feedback[] = [];
  let highlight: string | undefined;

  for (const raw of input.split(/[\s,;]+/).filter(Boolean)) {
    const parsed = parseEntry(raw);
    if (!parsed) continue;
    if ('error' in parsed) {
      feedback.push({ tone: 'error', message: parsed.error });
      rejected.push(raw);
      continue;
    }
    const { value, kind, note } = parsed;
    if (entries.includes(value)) {
      feedback.push({ tone: 'warning', message: `${value} is already in the list.` });
      highlight = value;
      continue;
    }
    const covering = `${WILDCARD}@${domainOf(value)}`;
    if (kind === 'address' && entries.includes(covering)) {
      feedback.push({ tone: 'warning', message: `${value} is already covered by ${covering}, so it was not added.` });
      highlight = covering;
      continue;
    }
    entries.push(value);
    if (note) feedback.push({ tone: 'info', message: note });
    if (kind === 'domain') {
      const covered = coveredBy(entries, domainOf(value));
      if (covered.length > 0) {
        feedback.push({
          tone: 'info',
          message: `${value} now covers ${covered.length === 1 ? '1 address' : `${covered.length} addresses`} already in the list.`,
          pruneDomain: domainOf(value),
        });
      }
    }
  }

  return { entries, rejected: rejected.join(', '), feedback, highlight };
};

/** The exact addresses at a domain, which a *@domain entry makes redundant. */
export const coveredBy = (entries: readonly string[], domain: string): string[] =>
  entries.filter((e) => !isDomainEntry(e) && domainOf(e) === domain);

export const pruneCovered = (entries: readonly string[], domain: string): string[] =>
  entries.filter((e) => isDomainEntry(e) || domainOf(e) !== domain);

/** `by` is the entry that admits the address, or null when the list is empty. */
export type Verdict = { allowed: true; by: string | null } | { allowed: false };

/** Whether a provider with these entries would send from an address. */
export const checkSender = (entries: readonly string[], from: string): Verdict | null => {
  const address = stripDisplayName(from).toLowerCase();
  const at = address.lastIndexOf('@');
  if (at <= 0 || at === address.length - 1) return null;
  if (entries.length === 0) return { allowed: true, by: null };
  if (entries.includes(address)) return { allowed: true, by: address };
  const covering = `${WILDCARD}@${domainOf(address)}`;
  if (entries.includes(covering)) return { allowed: true, by: covering };
  return { allowed: false };
};

/** IMAP and POP3 only receive, so there is no From to restrict. */
export const sendsMail = (type: ProviderType): boolean =>
  type !== ProviderType.IMAP && type !== ProviderType.POP3;

/**
 * Whether two domains plausibly share an organisation, for DMARC's relaxed
 * alignment. The server answers this exactly against the public suffix list;
 * here it is approximated by the last two labels, and only ever errs towards
 * staying quiet, so the form never warns about mail that would align.
 */
export const roughlyAligned = (a: string, b: string): boolean => {
  if (a === b || a.endsWith(`.${b}`) || b.endsWith(`.${a}`)) return true;
  const org = (d: string) => d.split('.').slice(-2).join('.');
  return org(a) === org(b);
};

export interface SmtpHint {
  tone: 'success' | 'info' | 'warning';
  message: string;
  /** An entry the hint offers to add. */
  add?: string;
}

/**
 * The SMTP-only advice under the list. The form knows two things on an SMTP
 * provider that decide whether a sender will actually work: the account it
 * signs in as, which most servers insist is the From, and the DKIM domain,
 * which every allowed domain has to align with for DMARC to pass.
 */
export const smtpHints = (entries: readonly string[], username: string, dkimDomain: string): SmtpHint[] => {
  const hints: SmtpHint[] = [];

  const user = username.trim().toLowerCase();
  const parsedUser = user.includes('@') ? parseEntry(user) : null;
  if (parsedUser && 'value' in parsedUser && parsedUser.kind === 'address') {
    const verdict = checkSender(entries, user);
    if (entries.length === 0) {
      hints.push({
        tone: 'info',
        message: `This server signs in as ${user}. Most SMTP servers only accept that address as the From, so it is a good first entry.`,
        add: user,
      });
    } else if (verdict?.allowed) {
      hints.push({ tone: 'success', message: `The signed-in account ${user} is covered by ${verdict.by}.` });
    } else {
      hints.push({
        tone: 'warning',
        message: `The signed-in account ${user} is not in the list, so it cannot send as itself. Most SMTP servers only accept that address as the From.`,
        add: user,
      });
    }
  }

  const dkim = dkimDomain.trim().toLowerCase();
  if (dkim) {
    const misaligned = entries.filter((e) => !roughlyAligned(domainOf(e), dkim));
    if (misaligned.length > 0) {
      hints.push({
        tone: 'warning',
        message: `DKIM signs as ${dkim}, so mail from ${misaligned.join(', ')} will fail DMARC alignment. Sign with that domain, or send it through a separate provider.`,
      });
    }
  }

  return hints;
};
