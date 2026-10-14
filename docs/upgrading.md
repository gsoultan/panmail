# Upgrading

What changed between releases that an operator or integrator has to act on, or
will at least notice. A change that is purely a fix, with nothing to do, is not
listed; read the release notes for those.

Each release gets a section, newest first. Read every section between the
version you run and the one you are moving to. Migrations run on their own at
startup, so a section only says something about the schema when it needs a
decision from you.

## Unreleased

### Providers restrict senders by address as well as domain

A provider's `allowed_domains` became `allowed_senders`: a list of exact
addresses (`billing@example.com`) and whole domains (`*@example.com`). Existing
restrictions carry over unchanged: a stored domain reads as `*@domain`.

| Change | What to do |
| :--- | :--- |
| `allowed_domains` is removed from `EmailProvider` and the create and update requests | Send and read `allowed_senders`, writing a domain as `*@domain`. A request that still sends `allowed_domains` has it ignored, so the provider is saved unrestricted. |
| The refusal reads `provider X is not authorized to send as alice@example.com` instead of `... to send for domain example.com` | Update anything that matches the old text. It is still `FailedPrecondition` over the API and still a permanent failure in the outbox. |
| Migration 0025 renames the `email_providers.allowed_domains` column to `allowed_senders` | Nothing, unless you query the table directly. During a rolling deploy, instances still on the old version fail to read providers once the migration has run, so roll forward rather than running the two versions side by side for long. |
| The web form now shows and saves the list | Nothing. Before this, saving a provider in the web form cleared a list set through the API. |

## v2.0.0 (from v1.6.x)

The October 2026 hardening pass, and the major version marks it: an
integration or deployment that worked on v1.6.x may need a change before it
works on v2.0.0, so read the table below first. Most of it tightens who may do
what and makes the gateway fail loudly where it used to fail late. The RPC API
itself is unchanged apart from the new optional `idempotency_key`.

### Before you upgrade

| Change | What to do |
| :--- | :--- |
| `/inbound/` needs `/inbound/{tenant_id}/{provider_id}` and the provider's webhook secret | Repoint each inbound parse integration and set a shared secret on its provider. Unsigned posts get `401`. |
| Inbound bounces count only when they are signed delivery reports, and inbound-parse services must post the raw message | Turn on "send raw" (SendGrid) or post `raw`; see [Bounces count only when they are real and signed](#bounces-count-only-when-they-are-real-and-signed). |
| A `mysql` or `mariadb` database type is refused at startup | Move to PostgreSQL or SQLite first. Those engines never worked past setup, so nothing on them was usable. |
| A gateway that cannot reach its database at boot exits after about two minutes | Run it under a supervisor that restarts it (systemd, Kubernetes, Docker `restart:`). |
| Shutdown waits `--shutdown-delay` (5s) before closing the port | Keep `terminationGracePeriodSeconds` at 30; use `--shutdown-delay=0` in development. |

### Inbound mail is authenticated

The endpoint used to be `POST /inbound/` with the tenant taken from the body,
and it accepted anything. It is now:

```
POST https://<your-gateway>/inbound/{tenant_id}/{provider_id}
```

authenticated with that provider's **webhook secret**, either as an
`X-Panmail-Signature: sha256=<hex HMAC-SHA256 of the body>` header or as HTTP
Basic credentials in the URL (`https://:<secret>@<your-gateway>/inbound/...`).
The tenant is the provider's; a `tenant_id` in the body is ignored. A post with
no valid credentials, to an unknown provider, or to a provider with no secret
gets `401`.

SendGrid and SES providers store a public key or a topic ARN, not a shared
secret, so they cannot authenticate inbound mail. Point inbound at a provider
whose webhook secret is a shared secret. The full contract is in the README,
[Inbound Processing](../README.md#inbound-processing).

### Deployment-wide controls belong to Super Administrators

Administrator is a tenant role. Anything that applies to every tenant at once
is now Super Administrator only:

- **System settings** (base URL, retention, redaction) and the **SMTP
  submission listener**. Have a Super Administrator make those changes.
- **Performance metrics** (`GetPerformanceMetrics`): CPU, memory, load, disk
  and the send rate across every tenant. The dashboard's System Health tab and
  Throughput card are hidden from other roles. Monitoring that polled the
  metrics with an Administrator's session needs a Super Administrator's.

### Two-factor belongs to the account's home tenant

An Administrator can no longer turn two-factor on or off for a **guest member**
of their tenant, an account whose home is another tenant. Two-factor guards the
person's sign-in to every tenant, so it is for an administrator of their home
tenant, or a Super Administrator, to change. Turning off your own second factor
is unchanged. The Users page disables the switch on a guest's row.

### Passwords need at least 12 characters

The setup wizard and every new account enforce it. Existing passwords keep
working until they are changed.

### Analytics buckets are UTC

Hour and day counters are bucketed in UTC regardless of the server's zone.
Nothing to do; on a server that was not running in UTC, graphs shift once by
its offset at the upgrade.

### The database must answer at boot

A configured gateway that cannot reach its database retries with backoff (1s
doubling to 15s) for about two minutes, then exits non-zero so its supervisor
restarts it. It used to stay up but never ready, with liveness green, so
nothing restarted it. A failed migration exits at
once rather than serving a half-migrated schema.

A configuration that can never work exits at once instead of retrying. That
includes a `mysql` or `mariadb` database type: the query layer uses
PostgreSQL-style positional parameters, which those engines reject, so the
error names PostgreSQL (`postgres`) and SQLite (`sqlite`) as the types to use.
The setup wizard does not offer them either.

### Graceful shutdown waits for the load balancer

From SIGTERM, `/readyz` answers `503` while the port stays open for
`--shutdown-delay` (default 5s), so a load balancer stops routing before
connections are refused. HTTP and workers then drain within the 30-second
termination grace. Keep `terminationGracePeriodSeconds` at 30 or more; set
`--shutdown-delay=0` for local development.

### Bounces count only when they are real and signed

A bounce read from an IMAP/POP3 mailbox or posted to `/inbound/` used to be
recognised by its wording, so anyone who could mail the mailbox could get any
address suppressed. A bounce now counts only when it is a delivery status
notification (a `message/delivery-status` part) whose returned headers carry
the `X-Panmail-Ref` header panmail signs onto every copy it sends, and the
event store shows that message sent to that address.

- Reports about mail sent before the upgrade carry no reference; they are
  stored as mail but suppress no one.
- A gateway with no auth key configured records no inbox bounces.
- An inbound-parse service must post the raw message (`email` form field, or
  `raw` in JSON) for a bounce to count. SES through SNS does this on its own.

### Inbound posts are deduplicated, and SendGrid and SES can sign in again

Migration `0020` adds `inbound_receipts`. A retried post to `/inbound/` is
stored once, keyed on the poster's `event_id`, else on the Message-ID and a
digest of the message. A retry while the first post is still being processed
gets `503` with `Retry-After`. A relay that posts mail without a Message-ID
should send an `event_id` to be retry-safe. Receipts are kept 7 days.

SendGrid and SES providers, refused on `/inbound/` since the hardening pass,
can authenticate again:

- **SendGrid:** add ` inbound=<user>:<pass>` after the event key in the
  provider's webhook secret, and put those credentials in the Inbound Parse
  URL (`https://<user>:<pass>@<your-gateway>/inbound/...`).
- **SES:** add the inbound SNS topic's ARN to the provider's webhook secret and
  subscribe the `/inbound/` URL to that topic; panmail confirms the
  subscription itself. Mail over 150 KB that SES stores in S3 arrives as its
  headers plus `X-Panmail-Ses-S3-Bucket` and `X-Panmail-Ses-S3-Object-Key`;
  panmail does not fetch it. SNS raw message delivery is refused.

### Sends can carry an idempotency key

Migration `0021` adds `send_idempotency_keys`. `SendEmail` accepts an
`idempotency_key` field or an `Idempotency-Key` header. A repeat with the same
key and request within 24 hours returns the first message id and queues
nothing; the same key with a different request is refused with
`already_exists`, and a repeat racing the first gets `aborted`, which is safe
to retry. Existing clients need do nothing.

SMTP submissions are de-duplicated on their Message-ID and envelope
recipients. A client that reuses one Message-ID for different messages to the
same recipients now gets `550 5.7.1` within a day of the first; give each
message its own Message-ID.

### Smaller changes you may notice

| Change | What to do |
| :--- | :--- |
| Deleting a tenant that still has users, keys or providers is refused, with a list of what remains | Remove those first. |
| Webhook deliveries follow redirects only within the subscription's own host | Point a subscription at its final URL. |
| `--webhook-block-private-targets` (off by default) keeps outbound webhooks off private, loopback, link-local and other reserved addresses | Set it on every instance if tenants should not reach your network. With it on, webhooks do not use `HTTP_PROXY`/`HTTPS_PROXY`. |
| An unreachable webhook endpoint is backed off as a whole: after two timeouts or refused connections in a row, all its waiting notifications wait for the next retry (migration `0022` adds an index) | Nothing; a dead endpoint's backlog is retried in bursts. |
