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
{"status":"not ready","failed":["database"]}
```

The name of the dependency, and deliberately nothing more. This endpoint is on
the public listener because a load balancer has to reach it, so its body
reaches anyone who asks — and the driver's error names the database's user,
host and port. The detail goes to the log instead, where the person debugging
is already looking.

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
- `auth.symmetric_key` — a token minted by one instance is verified by another,
  and it also keys the open, click and unsubscribe links in outgoing mail. The
  two failures do not look alike. A mismatched key logs everyone out, which is
  noticed within minutes; it also makes every tracking link the other instance
  issued answer `403 Invalid tracking link` for as long as those messages exist,
  and opens simply stop being recorded, because the pixel is served either way.
  Nothing reports that second one. Check it deliberately after any change: send
  a message from one instance and click its link against another.
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

### Delivery events can move to the shared database

That per-instance dashboard is a correctness problem at more than one instance,
and there are two flags for moving past it. Both default to off, and both are
reversible with a restart, because Pebble is still written either way.

| flag | effect |
| :--- | :--- |
| `-shadow-events` | writes events **and stored messages** to the database as well; reads stay on Pebble |
| `-shared-events` | reads them from the database; implies the above |

**Run the first for a while before the second.** That is the whole point of
having two: with the shadow on, `panmail_event_shadow_written_total` can be held
against what the dashboard reports, and `..._dropped_total` and
`..._failed_total` are the two ways they can legitimately differ. Both should be
zero. Switching reads before the counts have had time to disagree is skipping
the only step that can tell you whether they do.

`-event-dir` is still required with both flags on. The Pebble store keeps two
things that are genuinely per-instance and would be meaningless pooled: the
JSONL archives, which are files on this instance's disk, and the resource
metrics, which describe this process. A CPU figure averaged across three
gateways describes none of them.

Sizing note: measured at **~341 bytes per event** with both indexes, so roughly
1 KB per message on the database volume rather than the local disk. That moves
the growth conversation from `panmail_store_bytes_events` to the database, where
it is easier to alert on. `docs/design/0002-shared-event-store.md` has the
throughput measurements behind the change.

### The settings file used to be in both lists

`config.yaml` holds shared values — `auth.symmetric_key`, the database block —
and it used to hold mutable ones too: `app.base_url`, `app.retry_pattern` and
all seven retention policies. Settings -> Save wrote those back to the file, on
**one** instance. The others kept the old values until they were restarted, with
nothing to say they had diverged, and under Kubernetes the save failed outright
because a Secret volume is read-only.

They are in the database now, in a single `system_settings` row every instance
reads:

- **Retention** is re-read by the retention worker on every pass, and a save
  triggers a pass immediately on the instance that served it.
- **`base_url` and `retry_pattern`** are read through a snapshot refreshed once
  a minute (`system_settings.Provider`), because both sit on the send path —
  every tracking link needs the base URL, every deferral needs the pattern — and
  a query per message is the round trip the send path was measured to remove.

So a change reaches the instance that served it at once, and every other
instance within a minute. The config file is now deployment configuration only:
database, keys, paths.

**Upgrading carries your values across.** The first instance to start on this
version copies the settings out of `config.yaml` into the row and logs that it
did. It never overwrites, so restarts and rolling deploys cannot reset a value
an administrator has since changed. The fields stay in the file, unread, so a
rollback still finds them.

`config.yaml` must still be identical on every instance for the values that
remain in it. A signing key that differs between two instances means a token
minted by one is rejected by the other.

This is specific to the settings *file*. Per-tenant configuration -- a tenant's
`send_rate_per_minute`, a provider's `allowed_domains` and its credentials -- is
in the database, so an edit to any of it is visible to every instance as soon as
the caches expire.

## Deploys

A rolling deploy signals each instance in turn, and SIGTERM always lands while
the queue is busy. What happens then:

1. The listener stops accepting, and readiness starts failing.
2. The queue worker stops claiming new batches.
3. Sends **already in progress** are given ten seconds to finish.
4. Messages that were claimed but never started are left alone. They keep their
   claim, the lease expires, and the next instance to look picks them up.

Step 3 is the one that matters. A send cut off after the provider accepted DATA
but before its reply arrived has been delivered, and panmail has no way to know
— so the retry sends it a second time. Draining is what keeps a deploy from
being a duplicate-mail event. Measured on a 40,000-message queue: the same
signal used to cut off 184 in-flight sends, and now cuts off none.

Give the container at least 30 seconds to terminate, which is the Kubernetes
default:

```yaml
terminationGracePeriodSeconds: 30
```

Less than about 15 and SIGKILL arrives during the drain, which puts the
behaviour back where it was.

Delivery remains at-least-once across a restart, because the send and the
record of it are not one transaction. Draining shrinks the window; it does not
close it.

### Migrations during a deploy

Nothing special is required. Every instance runs the migrations on startup, and
a PostgreSQL advisory lock means one of them does the work while the rest wait
and then find nothing to do. There is no separate migration step to run and no
ordering to arrange.

Two consequences worth knowing:

- An instance will not finish starting until whichever instance holds the lock
  has finished migrating. That wait is deliberately unbounded — the alternative
  is starting to serve against a schema halfway through changing — so a long
  migration delays the whole rollout. Set readiness probe timeouts accordingly,
  or run a long migration deliberately rather than as part of a deploy.
- Migrations are not transactional across statements. A migration that fails
  partway leaves what it had already applied in place and the version
  unrecorded, so the next start retries the whole file. Additive steps tolerate
  "already exists"; anything else does not, and will need a hand.

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

This is a deliberate choice rather than an unfinished one. Making the bucket
shared would put a database write on every accepted message — the admission
path is entirely in memory today, since both the tenant's limit and its backlog
count are cached — and would force a decision about what happens when that
write fails. Failing open makes the limit stop applying without saying so;
failing closed stops all outbound mail because of a database blip. Neither is
better than doing the division. Revisit it if you autoscale, where there is no
fixed number to divide by.

## Inbound

Inbound polling and IMAP IDLE run in every instance, and they do not coordinate:
each will open its own IDLE connection per configured IMAP provider and each
will poll. Duplicate storage is prevented downstream — a message is keyed by
provider and UID, or by Message-ID within a tenant — so this is correct, but it
costs one IMAP connection per instance per provider, and some servers cap
concurrent connections per account.

If that matters, run inbound on a subset:

```sh
panmail -inbound=false    # on the replicas that only send
```

It defaults to **on**, because the alternative is a deployment that silently
stops receiving mail the first time somebody forgets the flag. Turn it off
deliberately, on the instances that do not need it.

An instance with inbound off **does not publish `panmail_imap_idle_sessions`**
at all, rather than publishing zero. Zero is the signal that inbound has
silently stopped, so a send-only replica reporting it would look exactly like
the failure the gauge exists to catch — and an alert on "no sessions while IMAP
providers are configured" would fire on every instance that was told not to
receive. Alert on the absence of a *receiving* instance, not on the metric being
absent here.

The `/inbound/` webhook endpoint is unaffected: it is an HTTP handler on the
public mux and still accepts deliveries on every instance. This flag governs
only the IMAP poll and IDLE connections that the gateway opens outward.

Running inbound on more than one instance stays correct either way — a message
is keyed by provider and UID, or by Message-ID within a tenant, so whichever
connection sees it first wins and the rest are no-ops. This is a flag about
connection count, not about correctness.

## The outbox is the highest-churn table in the schema

Every delivered message is inserted and then deleted. Nothing else here does
that at volume, so `outbox` accumulates dead tuples faster than any other table
and is the one worth tuning autovacuum for.

Measured after draining a million-message backlog: **78 MB of table with zero
live rows**, waiting on autovacuum to reclaim it. That is normal PostgreSQL
behaviour and not a fault — but at a million messages a day it is 78 MB of bloat
per million, and stock autovacuum settings are scaled for tables that mostly
grow rather than tables that churn.

The stock trigger is `autovacuum_vacuum_scale_factor = 0.2` — vacuum when 20% of
the table is dead. On a queue that is usually near-empty, 20% of "near-empty" is
a handful of rows, so it fires constantly and does little; during a backlog
drain, 20% of a million is 200,000 dead rows before it starts. Neither is what
you want. Pin it to a row count instead:

```sql
ALTER TABLE outbox SET (
  autovacuum_vacuum_scale_factor = 0.0,
  autovacuum_vacuum_threshold = 10000,
  autovacuum_vacuum_cost_delay = 2
);
```

That vacuums every 10,000 dead rows regardless of table size, which tracks churn
rather than volume. The cost delay keeps it from competing with the send path
for I/O.

Two things follow from the same measurement:

- **`pg_total_relation_size('outbox')` is not the queue depth.** Alert on
  `panmail_outbox_pending`, which counts rows. A large table with an empty queue
  is bloat awaiting vacuum, and the two need different responses.
- **The claim index shares the churn.** `idx_outbox_claimable` is partial and
  covers only claimable rows, so it stays small — but it is still rewritten as
  rows move through, and it is vacuumed with the table.

## Storage is what decides whether a deployment survives its volume

Measured over a four-hour run of 287,700 messages: **~4.7 KB per message**
across the event and application-log stores — roughly **4.7 GB per million
messages**, or 330 MB/hour at 1,200 messages a minute.

Retention was off for that run, so this is the unbounded rate rather than a
steady state. It is also the *normal* configuration: six of the eight retention
classes default to keeping data forever. That default is deliberate — panmail
holds the only copy of a received message or an archive, and a version bump must
never start deleting what nobody agreed to lose — but it means growth is the
expected case and not a fault.

The gateway says so once at startup, naming the classes:

```
INFO some data is kept forever by policy classes=message_retention_days,
     outbox_retention_days,app_log_retention_days,inbound_retention_days,
     archive_retention_days,quarantine_retention_days
```

and publishes a gauge per store:

```
panmail_store_bytes_events
panmail_store_bytes_logs
panmail_store_bytes_inbound
```

**Alert on the slope, not the size.** A store that is large because retention is
deliberately off looks identical to one filling up; only the rate of change
tells them apart. Something like a projected days-to-full is the useful alert,
and the absolute number is not.

`app_log_retention_days` is the one worth setting first. It is the larger half
of that 4.7 KB, it defaults to forever, and application logs are the class
nobody is keeping for its own sake.

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

## Watching a running gateway

`/metrics` publishes the queues and the process. The ones that answer "is this
still healthy in a week":

| Metric | Healthy | Not |
| --- | --- | --- |
| `panmail_goroutines` | spikes under load, returns to baseline | climbs and never returns |
| `panmail_heap_in_use_bytes` | a flat band | a rising floor |
| `panmail_db_connections_in_use` | below `max_open_conns` | pinned at it |
| `panmail_db_connections_wait_seconds_total` | grows slowly, or in step with wait_total | grows far faster than wait_total |
| `panmail_worker_restarts_total` | zero | anything else |
| `panmail_outbox_oldest_seconds` | seconds | minutes and rising |
| `panmail_retention_last_run_seconds` | under a day | over two |
| `panmail_retention_failures_total` | zero | anything else |

Three of those need explaining, because the obvious reading is wrong.

**Retention fails by not happening.** A pass runs daily and deletes what the
policy says; the way it goes wrong in production is not deleting the wrong
thing but quietly ceasing to run, which nothing else notices until a disk fills.
`retention_last_run_seconds` measures from process start until the first pass
completes, so it fires for a gateway where retention never ran at all rather
than reading as "ran just now". Each instance prunes its own Pebble stores, so
alert per instance.

#### What a retention pass costs, measured

`go test -tags soak -run TestRetentionSoak -timeout 30m -v
./internal/event/repositories/stores/pebble/` builds a store and expires 90% of
it. On a laptop, against 300,000 events and 60,000 messages with 8 KiB bodies —
474 MiB on disk:

| | |
| :--- | :--- |
| 270,000 events expired and archived | **6.0 s** |
| 54,000 message bodies deleted | **2.0 s** |
| Heap growth during the pass | **none measurable** |
| Live data (sstables) | 103.5 MiB → **9.8 MiB** |

The scan is bounded — it commits every 1,000 keys and does not hold the store —
so a pass costs seconds and a flat amount of memory whatever the store's size.

**But the directory will not shrink to match.** A Pebble store is sstables plus
write-ahead log, and in the same run the WAL was 370 MiB of the 474. The
memtable is 64 MiB and the writer commits without syncing, so most recent data
sits in a WAL rather than in any file a compaction can rewrite — and Pebble
recycles those files instead of deleting them, so they survive a restart. In
that run the settled total was 256 MiB, of which 246 MiB was WAL holding 9.8 MiB
of live data.

So: **size the disk for the WAL high-water mark, not for the retained data.**
Retention returns the live data and nothing else.

#### The lever, with numbers

`PANMAIL_PEBBLE_MEMTABLE_MB` sets the memtable size for all three Pebble stores
and defaults to 64, which is what panmail has always used. The memtable decides
how much sits unflushed in a WAL, so it decides the floor.

`go test -tags soak -run TestMemTableTradeoff -v
./internal/event/repositories/stores/pebble/` measures both directions. Writing
40,000 messages with 8 KiB bodies, median of three runs on a laptop:

| `MEMTABLE_MB` | writes/s | total on disk | live data | WAL floor |
| ---: | ---: | ---: | ---: | ---: |
| 64 (default) | 39,700 | 246 MiB | 26.4 MiB | 219 MiB |
| 32 | 39,500 | 114 MiB | 26.4 MiB | 88 MiB |
| **16** | **35,700** | **~65 MiB** | 26.4 MiB | ~39 MiB |
| 8 | 20,800 | ~28 MiB | 26.4 MiB | ~2 MiB |
| 4 | 20,800 | 26.4 MiB | 26.4 MiB | 0 MiB |

Live data is 26.4 MiB at every size, which is the point: everything above it is
overhead that no retention setting can touch.

**The throughput column is a red herring, and that is the finding.** Even the
slowest row writes 20,800 messages a second. Admission peaks near 3,000/s and
sustained delivery is 193/s, so the store is two orders of magnitude faster than
anything upstream of it can feed. Halving a number nothing was waiting on costs
nothing real.

**Recommended: 16.** Roughly a quarter of the disk for a throughput reduction
panmail cannot use. Go to 8 if disk is what is scarce — the floor essentially
disappears and the footprint becomes the data. Stay at 64 only if you have
measured your own workload and found the write path to be the constraint, which
these numbers say it is not.

```
PANMAIL_PEBBLE_MEMTABLE_MB=16
```

Values are clamped to 1..512, and an unreadable one falls back to the default
with a warning rather than refusing to start.

**Do not alert on RSS.** Go returns freed memory to the OS lazily, so RSS
climbs under load and comes back later. Measured over a 16-minute run of 57,500
messages: RSS peaked at 251 MB and settled at 26 MB once the load stopped,
while heap in use never left a 19–31 MB band. Alerting on RSS would have paged
for a leak that did not exist. `heap_in_use_bytes` is the one to watch.

**Connection waits are normal.** A send holds no database connection while it
is talking to SMTP — it takes one only to record the result — so two hundred
concurrent sends contending for twenty-five connections produces a large
`wait_total` while the queue still drains to zero. That is the pool working.
What is not normal is `wait_seconds_total` growing much faster than
`wait_total`, which means the waits have stopped being brief. That is when to
raise `max_open_conns`, or add an instance if the server cannot take more.

### Diagnosing a leak

`panmail_goroutines` climbing over a week says something is accumulating. It
does not say what. Profiling is served alongside metrics, under the same
loopback rule as the backup endpoint:

```sh
curl 'http://127.0.0.1:9090/debug/pprof/goroutine?debug=1' > goroutines.txt
go tool pprof -http=: 'http://127.0.0.1:9090/debug/pprof/heap'
```

Both are withheld when `--metrics-addr` is not loopback, and the startup log
says so. A heap profile is a dump of whatever the process is holding — message
bodies, decrypted provider credentials — and `/debug/pprof/profile` will spend
thirty seconds of CPU for whoever asks. Neither belongs on a port other
machines can reach.

If you need profiles from a gateway whose metrics are exposed for scraping,
reach it over an SSH tunnel or a sidecar rather than binding it wider.

## Deploying it

`deploy/kubernetes/panmail.yaml` and `deploy/prometheus/alerts.yaml` are a
starting point, not a drop-in. Every value in them is one this document
explains; the manifests carry the reasoning inline so you can change them
knowing what you are trading.

Two choices there are worth calling out because the obvious alternative is
wrong:

**A StatefulSet, not a Deployment** — and not for ordering. Each replica needs
its own Pebble directories, because Pebble takes an exclusive lock on a store
directory and the second replica to mount a shared volume will not start.
`volumeClaimTemplates` gives each pod its own.

**A memory limit near the heap figure will OOMKill a healthy process.** Heap in
use held 19–31 MB across a 16-minute run of 57,500 messages while RSS peaked at
251 MB before the runtime returned it. Size the limit against RSS; watch
`panmail_heap_in_use_bytes`.

The image keeps the metrics listener on loopback, so `/metrics`, `/admin/backup`
and `/debug/pprof` are all reachable only from inside the pod. Scrape with a
sidecar or a port-forward rather than binding it wider — the latter two take no
credentials.

## base_url is a deliverability setting

`app.base_url` builds the unsubscribe links that go into messages, and RFC 8058
requires that endpoint to be **https**. Anything else and the `List-Unsubscribe`
pair is omitted from every message — Gmail and Yahoo have required it from bulk
senders since February 2024, and they judge a whole sending domain by it, so
this is not one bad campaign.

The mistake this invites is specific to running behind a TLS-terminating proxy:
the gateway's own address is http, and configuring that here looks right. It is
not. `base_url` must be the public URL a recipient's mail client will open, not
the address the proxy dials.

```yaml
app:
  base_url: https://mail.example.com   # public, https
```

A gateway started with anything else says so once at startup:

```
WARN messages will be sent without one-click unsubscribe: app.base_url is not https
```
