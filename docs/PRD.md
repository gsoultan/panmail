# Panmail — Product Requirements

Status: living document. Last revised 2026-10-10, alongside the review that
hardened delivery classification, retention, tenant isolation and shutdown.

This is the "what and why" of panmail. The README is the "how to run it";
`docs/scaling.md` and `docs/backup.md` are the operator's reference; the
design records under `docs/design/` explain individual decisions.

## 1. Problem

Applications that send mail each end up rebuilding the same things: a
template system, retries that know a full mailbox from a dead one, a
suppression list so a bounced address is not tried forever, open and click
tracking, and a record of what happened to every message. Each copy is
partial, and each one talks to the mail provider directly, so changing
provider means changing every application.

Panmail is one self-hosted gateway that does this once, for many applications
and many tenants, in front of whatever SMTP servers or ESPs the operator
already uses.

```text
Application ──RPC / SMTP──▶ panmail ──SMTP / ESP──▶ recipient
                              │
                              └── events, webhooks, analytics, inbound mail
```

## 2. Who it is for

| Persona | Wants | Role in panmail |
| --- | --- | --- |
| **Operator** | One binary to deploy, back up and monitor; no surprise disk growth | Super Administrator |
| **Tenant administrator** | Run their own tenant's providers, keys, users and templates without seeing anyone else's | Administrator |
| **Template author / support** | Edit templates, providers, webhooks and suppressions; send a test | Editor |
| **Stakeholder** | Read analytics and logs, change nothing | Viewer |
| **Integrating developer** | One call to send, refusals they can act on, delivery outcomes pushed to them | API key with scopes |

A tenant is the isolation boundary. Nothing a tenant role does may read or
change another tenant's data, and nothing deployment-wide (system settings,
the SMTP listener, the unfiltered application log) is reachable below Super
Administrator.

## 3. Goals

1. **Accept a message once and own it.** A successful send means the message
   is durably in the outbox; delivery, retry and reporting are panmail's job.
2. **Never punish a recipient for a fault that is not theirs.** A provider
   outage, a bad SMTP password, greylisting or a content filter must not
   suppress an address. Only a statement about the recipient may.
3. **Bounded resources.** Every store, queue, cache and table has a limit or
   a retention. A gateway left alone for a year must not fill its disk or its
   heap.
4. **Tenant isolation by construction.** Data is keyed or filtered by tenant
   at the storage layer, and authority comes from the account as it is now,
   not from what a token said at sign-in.
5. **Lightweight.** A single Go binary with the UI embedded, SQLite for small
   installs and PostgreSQL for fleets, no other required service.

### Non-goals

- Being an MTA. Panmail relays through providers; it does not deliver to MX
  hosts itself.
- Marketing automation (segments, journeys, A/B tests).
- MySQL/MariaDB. The query layer uses PostgreSQL-style placeholders, so a
  `mysql` or `mariadb` database type is refused at startup and by the setup
  wizard, with an error naming PostgreSQL and SQLite.
- Exactly-once delivery to the recipient. An idempotency key (S8) makes
  *submission* safe to retry, but a send without a key — or an SMTP
  submission without a Message-ID — can still be duplicated by a lost
  acknowledgement, and a provider that accepts a message and then times out
  can still deliver it twice.

## 4. Functional requirements

### 4.1 Sending

| ID | Requirement |
| --- | --- |
| S1 | `EmailService.SendEmail` over ConnectRPC/gRPC/JSON, authenticated by an API key with the `email:send` scope, or by a console session. |
| S2 | An SMTP submission listener (off by default) feeding the same pipeline, with AUTH only over TLS on any non-loopback listener. |
| S3 | `provider_id` is required; panmail never guesses which provider a message leaves through. A `From` outside the provider's allowed senders (exact addresses or `*@domain`) is refused. |
| S4 | Refusals are decisions with stable codes (rate limited, backlog full, suppressed, invalid, unauthenticated, permission denied) and are made **before** anything is queued or recorded. Only `unknown` and `aborted` (S8) are worth retrying. |
| S5 | Each recipient receives its own copy (personal tracking), with the full visible To/Cc audience in the headers and only that recipient in the envelope. Bcc is never a header. |
| S6 | Handlebars and Go `html/template` bodies, with date formatting and time zones; a template that will not render is refused, not sent blank. |
| S7 | Attachments up to the configured request bound. |
| S8 | An optional idempotency key per send (`idempotency_key`, or the `Idempotency-Key` header; they must agree if both are set), at most 255 printable ASCII characters. Scoped to the tenant and remembered for 24 hours from acceptance, written in the same transaction as the outbox row. A repeat with the same request returns the first response and queues nothing; the same key with a different request is `already_exists`. Concurrent repeats queue once (unique key); a loser whose winner cannot be read yet gets `aborted`. Refusals store nothing. SMTP submissions use their Message-ID plus envelope recipients as the key. Expired keys are pruned by the outbox worker. |
| S9 | A reply keeps what a mail client relies on: `reply_to`, `in_reply_to`, `references` (trimmed to the root and the newest IDs when they will not fit one header line) and `disposition_notification_to` are sent as headers, and an attachment with a `content_id` is sent inline so the HTML can show it as `cid:`. An SMTP submission carries these from its own headers, dropping any that will not parse rather than refusing the message. |
| S10 | Sent copies, per tenant and chosen by a tenant administrator: None (the default), Shared admin login (one IMAP master user files the copy in each sender's Sent folder as `<sender><separator><master>`), Per-user login (each sender's own mailbox login, stored encrypted) or Bcc to sender (the copy is mailed back through the message's provider). SMTP submissions ask for a copy; API callers opt in with `save_sent_copy`. Copies are queued durably, filed in the background with retries, never logged in for without TLS, and the setting is read when the copy is filed, so switching to None stops copies already waiting. The login modes need a data key. |

### 4.2 Delivery

| ID | Requirement |
| --- | --- |
| D1 | A durable outbox. Rows are claimed with a lease (`FOR UPDATE SKIP LOCKED` on PostgreSQL); a lease is confirmed before sending and renewed while a message is in flight, so two instances never send the same row. |
| D2 | Each recipient is settled on its own outcome. A recipient already delivered or settled is never sent to again; if the delivery history cannot be read, nothing is sent and the message is deferred. |
| D3 | Each send attempt has its own timeout. A pass that delivered to anyone does not consume a retry. |
| D4 | Errors are classified from the server's reply, not the whole error string: addresses and IPs are ignored, enhanced status codes are matched whole, any 4xx is temporary, and only explicit statements about the recipient (unknown user, complaint, unsubscribe) blame the recipient. |
| D5 | Hard bounces, complaints and unsubscribes suppress the address; soft bounces never do. Suppression is re-checked per recipient at delivery, so an unsubscribe stops mail already queued. |
| D6 | Per-tenant retry patterns (e.g. `5m, 1h, 1d`) and send-rate limits, plus per-provider pacing. |

### 4.3 Events, analytics and webhooks

| ID | Requirement |
| --- | --- |
| E1 | Every lifecycle step (PENDING, SENT, DELIVERED, OPENED, CLICKED, BOUNCED, …) is an event keyed by `message_id`. PENDING is recorded only once a message is queued. |
| E2 | Signed open/click/unsubscribe links; the click endpoint is not an open redirect. |
| E3 | Analytics counters by minute, hour and day, in UTC. Ranged queries read the coarsest buckets that cover the range. Hour and day counters are kept forever unless `analytics_retention_days` is set. |
| E4 | Outbound webhooks: durable, signed, leased like the outbox, delivered with bounded parallelism across subscriptions, and only redirected within the subscription's own host. A batch is claimed round-robin across subscriptions with a per-subscription cap, and an endpoint that times out or refuses connections twice in a row is backed off as a whole, so no one endpoint can occupy the workers or the batch. Targets on private addresses are allowed by default (self-hosted receivers); `--webhook-block-private-targets` refuses private, loopback, link-local, CGNAT, multicast and unspecified addresses (IPv4, IPv6 and IPv4-mapped) on the address actually dialled, for every connection and redirect, and refuses such a URL when a subscription is saved. |
| E5 | Inbound ESP webhooks (SendGrid, Mailgun, Postmark, SES, generic HMAC) are verified before any event is recorded. |

### 4.4 Inbound mail

| ID | Requirement |
| --- | --- |
| I1 | IMAP/POP3 polling and IMAP IDLE per provider; an IDLE session restarts when its provider's settings change. |
| I2 | An HTTP parse endpoint at `/inbound/{tenant_id}/{provider_id}`, authenticated with the provider's webhook secret (HMAC header or HTTP Basic). SendGrid Inbound Parse authenticates with HTTP Basic against an `inbound=` credential kept beside the event key in the same field; SES receipt rules post through SNS and are authenticated by SNS's signature (versions 1 and 2) from a topic the provider names, with the signing certificate fetched only from that topic's regional SNS endpoint and a subscription confirmed only for that topic. The tenant comes from the credential, never from the body. JSON, multipart and urlencoded bodies are accepted; anything unparseable is refused. A retried post is stored once: deduplicated per tenant and provider on the poster's event id, else on the Message-ID with a digest of the message, through a receipt in the shared database that holds under concurrent posts and on any instance. A duplicate is answered as a success; one whose first post is still in flight is asked to retry. |
| I3 | A received bounce is recorded only when it is a delivery status notification (a `message/delivery-status` part) whose returned headers carry the `X-Panmail-Ref` panmail signed onto that copy, verifying for the receiving tenant and the reported `Final-Recipient`/`Original-Recipient`, and the event store shows that message sent to that address. It is recorded against that address and message only; never against the sender. Anything else is stored as mail and suppresses no one. |
| I4 | A received message is committed before it is acknowledged. |

### 4.5 Filtering and review

| ID | Requirement |
| --- | --- |
| F1 | Per-tenant inbound and outbound rules (allow, reject, tag, hold for review), compiled once per tenant and cached briefly. |
| F2 | Held messages wait in a review queue; on expiry the held payload is discarded, and finished review records are pruned on the quarantine retention. |
| F3 | A rule can match the text inside a PDF, Word (.docx) or Excel (.xlsx) attachment. The text is read only when a tenant's rule asks for it, and the read is bounded (25 MB per file, 1 MB of text, 64 MB decompressed, 1000 PDF pages). An attachment that cannot be read (encrypted, scanned, legacy .doc/.xls) has no text and does not match. Inbound messages do not yet pass their attachments to the filter, so today this applies to outbound mail. |

### 4.6 Administration

| ID | Requirement |
| --- | --- |
| A1 | Four roles (Super Administrator, Administrator, Editor, Viewer). One account may belong to several tenants, with a role per tenant. |
| A2 | A session's role and tenant are re-read from the account (cached a few seconds), so deletion and demotion take effect promptly. If the account cannot be read, the request is answered 503, not 401. |
| A3 | Administrators cannot demote, delete or disable two-factor for a Super Administrator, nor act on users outside their tenant. Two-factor belongs to the account, so an Administrator changes it only for users whose home tenant is theirs, not for guest members. |
| A4 | System settings, the SMTP listener and the host's performance metrics are Super Administrator only. Application logs are scoped: a tenant sees only entries that name it; only a Super Administrator sees the unfiltered log. |
| A5 | Passwords are at least 12 characters, for the first admin and every later account. TOTP two-factor; sign-in and TOTP attempts are limited per account and hold under concurrent bursts and floods. |
| A6 | First-run setup writes its config only after the admin exists, so a failed setup can be retried. |
| A7 | A tenant that still holds data cannot be deleted; the refusal says what remains. |

## 5. Non-functional requirements

### 5.1 Performance

Delivery is bound by provider round trips, not CPU. The measured soak
sustained ~193 sends/s on one instance with `outboxConcurrency = 200`;
throughput is a function of the provider's latency and limits, and operators
should benchmark their own setup rather than rely on a headline figure.

Paths that must not regress. Each is guarded twice: by an ordinary test that
pins its shape (a round-trip count, an allocation budget), which runs on every
change, and by a benchmark that the nightly `bench` workflow compares against
the previous `main` with benchstat, failing on more than 25% more time/op or
allocs/op at p < 0.05. Per-change CI does not run benchmarks; a branch is
compared on demand.

- Admission costs one suppression round trip whatever the recipient count, and
  stays inside an allocation budget.
- API-key verification is served from a short, bounded, revocation-aware cache.
- Recording an event does not read the stored message body when the caller
  already supplied what the event needs.
- Filter rules are compiled once per tenant, not once per message.
- Link rewriting is a single regexp pass per body.
- A ranged metrics total is tiled from day counters, reading hour and minute
  counters only at the ragged edges, so its cost follows the number of days.

### 5.2 Resource bounds

| Resource | Bound |
| --- | --- |
| Template parse caches | 512 entries per engine, 10-minute TTL, keyed by source hash |
| Auth, provider, retry-pattern caches | `pkg/cache` TTL caches with entry limits |
| Sign-in limiter | Bounded map with LRU eviction that never frees a locked account |
| Event write buffer | 20,000 ops, drained on shutdown; writes after close are refused |
| Log store | Async, drained on shutdown; one List call reads at most 20,000 entries |
| Inbound listing / template listing | Page size defaults to 50, capped at 500 |
| Minute-level analytics and resource samples | Pruned on a fixed horizon (minutes: 30 days, or the analytics retention if shorter) |
| Hour and day analytics counters | `analytics_retention_days`; zero, the default, keeps them forever |
| Held outbox payloads, review history | Pruned on the quarantine retention |
| Inbound endpoint receipts | Pruned after 7 days, a fixed horizon past every provider's retry schedule |
| Everything else | The retention settings (README, "Data retention"); zero means keep forever |

### 5.3 Reliability

- Startup retries the database with backoff and exits if it never comes up, so
  a supervisor restarts it; a configuration that can never work (an
  unsupported engine such as MySQL/MariaDB, a SQLite database with no path)
  exits at once. A failed migration stops the process rather than serving a
  half-migrated schema.
- Shutdown: readiness fails first, streams end, HTTP and workers drain within
  the 30-second termination grace, then provider pools and Pebble stores close.
- Liveness (`/healthz`) checks nothing external; readiness (`/readyz`) checks
  the database, cached briefly and logged only on change.

### 5.4 Security

- Secrets: API keys stored as SHA-256 hashes; provider credentials and TLS
  keys encrypted with AES-256-GCM under `PANMAIL_SECRET_KEY`.
- Every unauthenticated HTTP surface (`/inbound/`, `/webhooks/`, tracking
  links) verifies a secret or signature before acting.
- No tenant-supplied value selects another tenant's data.

## 6. Client SDKs

The Go and PHP clients live in
[gsoultan/panmail-sdk](https://github.com/gsoultan/panmail-sdk). Requirements
they share:

- One call to send; typed refusals; no retry of an unknown outcome unless the
  send carries an idempotency key (S8); an opt-in retry of rate-limit refusals.
- Bounded responses: at most 1 MiB is read, at most 64 KiB more is discarded,
  and refusal messages are cut to 4 KiB.
- Refusals are classified from status and code even when the body was cut
  short.
- Webhook verification in constant time with a two-sided timestamp tolerance.

## 7. Known gaps

- A bounce report counts only when it returns panmail's signed reference, so
  reports about mail sent before the reference existed, from a gateway with no
  auth key, or through an inbound-parse service that does not post the raw
  message are not recorded. A recipient who passes on a copy with its headers
  intact hands over the means to bounce their own address, and only theirs.
- A send that loses an idempotency-key race (S8) has already been charged
  against the tenant's send rate, and a `tag` filter rule may have recorded it,
  though nothing was queued for it.
- SES mail delivered by a receipt rule's S3 action (any message over 150 KB)
  is stored with its headers and the bucket and key only; panmail does not
  fetch it from S3. SNS raw message delivery cannot be verified and is refused.
- A post to the HTTP inbound endpoint that carries neither an event id nor a
  Message-ID is not deduplicated, and neither is a retry that arrives more than
  7 days after the first post, when its receipt has been pruned.
- Webhook URLs may target private addresses unless the operator starts the
  gateway with `--webhook-block-private-targets`; that is a block-list of
  reserved ranges, not an allow-list of destinations, and with it on the
  environment's HTTP proxy is not used for webhooks.
