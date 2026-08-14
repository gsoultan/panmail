# Running more than one gateway

A gateway holds no state that another one needs. Everything durable is in the
SQL database or the local Pebble stores, and the outbox hands each message to
exactly one worker, so running several instances behind a load balancer is a
matter of starting them and pointing them at the same database.

There is one number that has to be right first.

## The connection arithmetic

Each instance opens its own pool. The pools do not know about each other, but
the server they share has a fixed budget:

```
instances × database.max_open_conns + headroom ≤ server max_connections
```

`headroom` covers whatever else talks to that database: a migration on deploy,
an operator's `psql`, a backup job, and PostgreSQL's own superuser reservation
(`superuser_reserved_connections`, 3 by default).

The default is 25 per instance. A stock PostgreSQL allows 100, so the default
fits three gateways with 22 connections to spare. Beyond three, either raise
`max_connections` on the server or lower the per-instance bound:

```yaml
database:
  type: postgres
  host: db.internal
  port: 5432
  user: panmail
  dbname: panmail
  max_open_conns: 15   # 6 instances × 15 = 90, leaves 10
```

Check what the server allows before doing the sum:

```sh
psql -c 'SHOW max_connections'
psql -c 'SHOW superuser_reserved_connections'
```

Lowering the bound does not lose throughput the way it looks like it should.
The pool queues rather than fails, and the work is dominated by talking to SMTP
providers, not by talking to the database — two instances at 25 each drained a
21,000-message queue with the pool never becoming the limit.

## Why this is the first thing to get right

Overcommitting is not a clean failure. Getting it wrong sends duplicate mail.

The bound used to be hard-coded at 100 — the server's entire default budget —
so one instance was sized to take every slot. Running two produced this, at
volume:

```
ERROR failed to delete outbox email
  error="server error: FATAL: sorry, too many clients already (SQLSTATE 53300)"
```

The message had already been accepted by the provider. Only the delete of the
outbox row failed, so the row stayed `PENDING`, its lease expired, another
worker claimed it, and the recipient got a second copy. In a two-instance run of
21,000 messages, six arrived twice and 59 database operations failed. Nothing
about that is visible as an outage: the gateway is up, the queue drains, and the
damage is in someone's inbox.

With the bound at 25 the same run delivers 21,000 distinct messages, none
duplicated, none missing, with no errors on either instance and 51 of the
server's 100 connections in use.

Delivery is still at-least-once — the send and the record of it are not one
transaction, and nothing can make them one — but a transient database error no
longer costs a duplicate. The bookkeeping writes after a send retry briefly
before falling back to the lease.

## Health checks

Two endpoints, and pointing the wrong one at the wrong probe causes an outage
rather than preventing one.

| Endpoint | Asks | Failing means |
| --- | --- | --- |
| `/healthz` | Is this process still working? | Restart it |
| `/readyz` | Can it serve a request right now? | Stop sending it traffic |

`/healthz` deliberately checks nothing external. A database failover fails
every instance's dependency check at the same moment; if that were wired to
liveness, the orchestrator would restart the whole fleet against a database
that is already struggling, and every pool and cache would come back cold.

`/readyz` pings the database and answers with what failed:

```
$ curl -s localhost:8080/readyz
{"status":"ready"}

$ curl -s localhost:8080/readyz          # database down
{"status":"not ready","failed":["database"],
 "details":{"database":"... connect: connection refused"}}
```

It returns 503 in that state and 200 again once the database is back, with no
restart in between. In Kubernetes:

```yaml
livenessProbe:
  httpGet: { path: /healthz, port: 8080 }
readinessProbe:
  httpGet: { path: /readyz, port: 8080 }
  periodSeconds: 5
```

Because readiness takes a connection from the pool, it also reports the case
where the server is fine and this instance cannot reach it because its own pool
is saturated. That is the intended answer: an instance that cannot get a
connection within two seconds cannot serve a request either.

## Encrypting the database connection

`sslmode` defaults to `prefer`: TLS wherever the server offers it — which every
managed PostgreSQL does — and a plain connection to one that does not. That is
a floor, not a destination. `prefer` falls back silently and verifies no
certificate, so anything crossing a network you do not own wants:

```yaml
database:
  ssl_mode: verify-full
```

On startup, a connection that ends up unencrypted to a non-loopback host logs:

```
WARN the database connection is not encrypted host=db.internal sslmode=prefer
```

Everything panmail stores crosses that socket: message bodies, recipient
addresses, and API keys and session material as they are read back. Stored
credentials are separately encrypted at rest under `PANMAIL_SECRET_KEY`, which
is worth having and does nothing for the wire.

## What each instance needs of its own

Shared, and must be the same everywhere:

- the SQL database
- `auth.symmetric_key` — a token minted by one instance is verified by another
- `PANMAIL_SECRET_KEY` — the key stored credentials are encrypted under. An
  instance without it cannot read any provider configuration.

Per-instance, and must **not** be shared:

- `-log-dir`, `-event-dir`, `-inbound-dir`. These are Pebble stores, and Pebble
  takes an exclusive lock on its directory: a second instance pointed at the
  same path fails to start. On separate hosts this is automatic; on one host,
  give each instance its own paths.
- `-metrics-addr` and `PORT`.

Because the event and log stores are per-instance, the dashboard reads whatever
the instance serving the request has seen. Point Prometheus at every instance's
`/metrics` and aggregate there rather than expecting one instance to hold the
whole picture.

## Send rate limits are per instance

`send_rate_per_minute` is enforced by an in-memory token bucket, so each
instance keeps its own. Three instances give a tenant configured for 1000/min
an actual ceiling of 3000/min.

That matters because the limit usually exists to stay inside somebody else's:
a provider that starts deferring or blocking above a rate does not care that
the excess came from three processes. Divide the configured limit by the number
of instances, and remember to revisit it when that number changes.

The bound worth keeping exact is the queue depth ceiling, which is a count of
rows and therefore already shared. It is only the rate that multiplies.

## Inbound

Inbound polling and IMAP IDLE run in every instance, and they do not coordinate:
each will open its own IDLE connection per configured IMAP provider and each
will poll. Duplicate storage is prevented downstream — a message is keyed by
provider and UID, or by Message-ID within a tenant — so this is correct, but it
costs one IMAP connection per instance per provider, and some servers cap
concurrent connections per account.

If that matters, run inbound on one instance. There is no flag for it yet; the
practical approach is to configure IMAP providers in a tenant served by a single
instance.

## Verifying a deployment

The check that matters is that each message is delivered once. Fill the queue
past `outboxBatchSize` (500) so that every instance is claiming at the same
time, then compare what arrived against what was queued:

```sh
# with a recipient that records what it receives
scripts/soak/sink.py 2599
```

`scripts/soak/README.md` has the details. A run that drains with equal batch
counts across instances and no duplicates at the far end is the property this
page is about.
