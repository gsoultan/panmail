# Upgrading

What changed between releases that an operator or integrator has to act on, or
will at least notice. A change that is purely a fix, with nothing to do, is not
listed; read the release notes for those.

Each release gets a section, newest first. Read every section between the
version you run and the one you are moving to. Migrations run on their own at
startup, so a section only says something about the schema when it needs a
decision from you.

## Unreleased (after v1.6.1)

The October 2026 hardening pass. Most of it tightens who may do what and makes
the gateway fail loudly where it used to fail late.

### Before you upgrade

| Change | What to do |
| :--- | :--- |
| `/inbound/` needs `/inbound/{tenant_id}/{provider_id}` and the provider's webhook secret | Repoint each inbound parse integration and set a shared secret on its provider. Unsigned posts get `401`. |
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

### Smaller changes you may notice

| Change | What to do |
| :--- | :--- |
| Deleting a tenant that still has users, keys or providers is refused, with a list of what remains | Remove those first. |
| Webhook deliveries follow redirects only within the subscription's own host | Point a subscription at its final URL. |
