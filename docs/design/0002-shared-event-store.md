# 0002 — Moving the event store off each instance

**Status:** Steps one to three implemented behind flags. Step four deliberately not taken — see §8.
**Scope:** Whether delivery events should move from per-instance Pebble to the
shared SQL database, and what it would cost.

---

## 1. The problem

`events.db` and `logs.db` are local Pebble stores. Each gateway writes only what
it handled, and reads only what it wrote.

With one instance that is invisible. With three, the dashboard and the analytics
endpoints show whatever the instance that served the request happened to see —
roughly a third of the truth, with nothing to indicate it. A message sent
through replica A has no timeline on replica B. Delivery rates are wrong by
however the load balancer split the traffic.

This is the only remaining item in panmail that is a correctness problem rather
than a documented limitation. `docs/scaling.md` describes the behaviour; it does
not make it acceptable.

## 2. Why it was built this way

The send path writes events, and it writes several per message:

```
PENDING -> SENT -> DELIVERED        (plus DROPPED or DEFERRED on failure)
```

Six `RecordEvent` call sites in `send_email_usecase.go`, around three events per
message in the common case.

`store.Write` does not touch disk. It pushes onto a 20,000-deep channel and
returns; a background worker batches 500 operations or 100 ms, whichever comes
first, and commits. **The send path's cost for an event is one channel send.**

That is why the store is where it is, and it is the property any replacement has
to keep. It is also why "just write to Postgres instead" would be the wrong
change if it were done synchronously — it would put a database round trip behind
every event, three per message, on the path that two prior releases were spent
making round-trip-free.

Worth noting for the comparison below: the current commit is `batch.Commit(nil)`
— **no fsync**. A crash loses up to 500 events or 100 ms of them, whichever is
smaller. The present design is fast and per-instance *and* not durable.

## 3. What it would cost — measured

The question is whether a batched writer against PostgreSQL can absorb the rate
the send path produces. Measured against PostgreSQL 16 with the table and the
two indexes the dashboard's queries need:

| batch size | throughput |
| :--- | :--- |
| 500 (what the Pebble writer uses today) | **31,248 events/s** |
| 2,000 | **111,987 events/s** |

Against demand: the fastest drain measured on one instance is 2,430 msg/s, which
at three events per message is **~7,300 events/s**.

| | |
| :--- | :--- |
| headroom at batch 500 | **4.3×** |
| headroom at batch 2,000 | **15.4×** |

Reads, on 400,000 rows:

| query | plan | time |
| :--- | :--- | :--- |
| a tenant's recent events, paginated | `Index Scan` on `(tenant_id, timestamp DESC)` | **0.044 ms** |
| the timeline for one message | `Index Scan` on `(message_id)` | **0.054 ms** |

Storage: **~341 bytes per event**, including both indexes.

## 4. What the measurement changes

**The throughput concern was unfounded.** The reason to hesitate was that the
per-instance store is what keeps the send path fast, and moving to the shared
database would trade throughput for correctness. It would not. The send path
keeps its channel send either way — only the worker behind the channel changes
backend, and that worker has four to fifteen times the headroom it needs.

Three things get *better* rather than worse:

- **Durability.** `Commit(nil)` loses up to 500 events on a crash. A PostgreSQL
  commit does not.
- **Retention.** Event retention is a Pebble key-range prune per instance today.
  It becomes an ordinary `DELETE` the retention worker already knows how to do.
- **Backup.** `events.db` needs the Pebble checkpoint path in `docs/backup.md`.
  A table does not.

## 5. What it costs that the numbers do not show

- **The database becomes the bottleneck for two things instead of one.** Event
  writes share the pool with the outbox claim. At batch 500 and 7,300 events/s
  that is ~15 connection acquisitions per second against a pool of 25 — small,
  but it is not zero, and `docs/scaling.md`'s connection arithmetic would need
  revisiting.
- **The outbox is already the highest-churn table.** Adding a second
  insert-heavy table with the same lifecycle doubles what autovacuum has to keep
  up with. The tuning note in `docs/scaling.md` would need to cover both.
- **Growth moves from a local disk to the database volume.** ~341 bytes/event is
  ~1 KB per message, so a million messages a day is ~1 GB/day in PostgreSQL
  rather than on the instance. That is easier to alert on and harder to ignore,
  which is an improvement, but it is a different capacity conversation.
- **`logs.db` is a separate question.** It is application logs, not delivery
  events; it is the larger of the two stores and nobody queries it across
  instances. It should stay where it is.

## 6. Recommendation

Move `events.db` to the shared database. Leave `logs.db` and `inbound.db` alone.

Keep the existing shape exactly: `Write` stays a channel send, the worker stays
a batching loop, only its commit changes. The batch size stays 500 — the
measurement says 2,000 is faster, but 500 already has 4.3× headroom and a
smaller batch loses less on a crash and holds a connection for less time.

Sequencing, so a bad step is reversible:

1. Add the table and the two indexes as a migration. Nothing reads it yet.
2. Write to both stores, read from Pebble. Compare counts under load.
3. Switch reads to SQL. Pebble is still written, so a revert is a one-line change.
4. Stop writing Pebble, and drop `-event-dir` from the per-instance list in
   `docs/scaling.md`.

Step 2 is what makes this safe, and it is the step to resist skipping: it is the
only point at which the two stores can be compared against the same traffic.

## 8. What was actually built, and how step four changed

Steps one to three are done and behind two flags:

| flag | what it does |
| :--- | :--- |
| `-shadow-events` | writes events **and stored messages** to both stores; reads stay on Pebble |
| `-shared-events` | reads events and messages from the shared store; implies the above |

Both default to off, and turning either off is a restart — Pebble is still
written in both modes, so nothing is one-way.

**Step four is not "stop writing Pebble", and this document was wrong to say
so.** `events.db` holds three things, not one:

| | where it ended up | why |
| :--- | :--- | :--- |
| delivery events | shared database | every instance must agree |
| stored message bodies | shared database | the Content tab is per-instance without it |
| JSONL archives | **stays local** | files on this instance's disk |
| resource metrics | **stays local** | measurements of *this* process |

So `-event-dir` cannot be dropped from the per-instance list. It is still a
Pebble store; it just holds less. Pooling an archive across instances would
mean pooling a filesystem, and a CPU reading averaged across three gateways
describes none of them.

Stopping the Pebble event writes entirely is a fifth step nobody should take
until the shared store has run as the read path for long enough to trust,
because it is the one step that cannot be reversed by a restart.

### The gap end-to-end testing found

Shadowing was written for events alone. That produced a shared store with every
timeline and no bodies — and switching reads to it gave a working dashboard
with a **blank Content tab** for everything written before the switch. Caught by
running the two phases against one gateway and opening a message, not by any
test.

`ShadowWriter` covers both halves now. The lesson is the general one: a shadow
that covers part of what a reader needs is not a shadow, and the only way to
find that is to read from it.

### Two engine differences worth recording

**`ILIKE` is PostgreSQL's**, and a syntax error on SQLite, whose `LIKE` is
already case-insensitive for ASCII. Same divergence that made provider search a
reason to run `--db postgres`.

**`strftime` returns NULL on every row here.** SQLite stores what the driver
gives it, and that is Go's `String()` form —
`2026-09-06 08:08:21.841566 +0000 UTC` — which it cannot parse. It fails
silently, as an empty chart rather than an error. The bucket is a `substr` of
the ISO-ordered prefix instead. This is the same trap `parseStoredTime` exists
for on the outbox, met a second time in a different place.

## 7. Open questions

- **Archives.** Expired events are written to per-tenant JSONL archives before
  deletion. That path reads from Pebble and would move with it.
- **`GetPerformanceMetrics`** aggregates from the event store. Whether it stays
  a query or becomes a materialised rollup depends on how it behaves at tens of
  millions of rows, which is not measured here.
- **Do the resource metrics move too?** `WriteResourceMetric` shares the store
  but is genuinely per-instance data. It should probably stay.
