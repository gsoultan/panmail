# Backup and restore

Verified end to end on 2026-08-14: a backup taken from a running gateway was
restored into a fresh location, and the restored instance signed in with the
original credentials and decrypted its provider secrets.

## What has to be kept

Four things, and a backup missing any one of them restores into something that
starts but does not work:

| | |
|---|---|
| The SQL database | tenants, users, API keys, providers, templates, webhooks, suppressions, the outbox, webhook deliveries |
| `events.db` | delivery events and analytics |
| `inbound.db` | received mail |
| `logs.db` | the log history the UI reads |
| `config.yaml` | the auth signing key — without it every existing session and API key is void |
| **The data encryption key** | `PANMAIL_SECRET_KEY`. Without it every provider password, DKIM private key, OAuth token and webhook secret is permanently unreadable. |

The key is the one that cannot be recovered. Everything else can be rebuilt
from a replica or, at worst, re-entered; a lost data key means re-entering every
credential by hand, from the customer, one at a time.

## Taking one

**From a running gateway** — the normal case. The Pebble stores are held
exclusively by the process, so it is the only thing that can snapshot them
without downtime:

```bash
curl -X POST 'http://127.0.0.1:9090/admin/backup?out=/var/backups/panmail-2026-08-14'
```

The endpoint is on the loopback-only admin listener, alongside `/metrics`. It
returns the manifest it wrote.

**With the gateway stopped** — for a cold copy:

```bash
panmail backup --config /etc/panmail/config.yaml --out /var/backups/panmail-2026-08-14
```

This refuses rather than proceeding if any store is locked. A backup that
quietly contains a quarter of the data is worse than one that failed.

Neither path copies a PostgreSQL database: run `pg_dump` and keep the output
alongside. `pg_dump` understands roles, sequences and extensions, and a worse
reimplementation here would be a liability.

## Why not just copy the directories

Both engines are being written to while you copy them. A filesystem copy of an
open Pebble store captures memtables that were never flushed and SSTables
mid-compaction; the result opens cleanly and is missing writes. SQLite in WAL
mode has the same problem. The backup uses each engine's own snapshot mechanism
— `Checkpoint` and `VACUUM INTO` — which is the whole reason it exists as a
command rather than a line in a runbook telling someone to use `cp`.

## The manifest

`manifest.json` is written last, so a directory without one is an unfinished
backup rather than a usable one. It records the **fingerprint** of the key the
credentials are encrypted under — never the key itself, since a backup carrying
its own key protects nothing:

```json
{
  "secret_key_id": "f29d0772",
  "stores": ["panmail.sqlite", "events.db", "inbound.db", "logs.db", "config.yaml"]
}
```

Check that fingerprint against the key you hold before restoring. Restoring
onto the wrong one produces a working gateway full of unreadable credentials
and a decryption error nobody can place.

## Restoring

1. Put the four stores where the new instance expects them.
2. Restore `config.yaml`, or copy the `auth.symmetric_key` from it into the
   new one. Skipping this invalidates every session and API key.
3. Set `PANMAIL_SECRET_KEY` to the key named in the manifest.
4. Start the gateway and check two things — that you can sign in, and that a
   provider's host still reads correctly. The first proves the auth key came
   across, the second proves the data key did.

Step 4 is the test. A restore that has not been checked is a hypothesis.

The mechanism is checked on every run of the suite —
`internal/backup/restore_test.go` takes a backup of a Pebble store and a SQLite
database while both are being written to, then opens the output and reads every
key and row back. That covers whether the snapshots are coherent. It does not
cover your deployment: steps 2 and 3 are about keys this machine does not have,
and only signing in against a restored copy proves those came across.

One thing that test also establishes: the snapshot succeeds under concurrent
writes *because* the gateway's SQLite connection carries a busy timeout and
WAL. Against a database opened without them, `VACUUM INTO` fails outright with
`SQLITE_BUSY` the moment anything else is writing. If you are backing up by
some other route, use `pkg/db.Connect` rather than opening the file yourself.

## The backup endpoint and the metrics listener

`POST /admin/backup` is served on the metrics listener and takes no
credentials. That is safe because the listener defaults to `127.0.0.1:9090`,
and a caller who is already on the box is already an operator.

It stops being safe if you bind that listener anywhere else — which
`--metrics-addr 0.0.0.0:9090` does, and which is the obvious way to let
Prometheus scrape from another host. So panmail does not mount the endpoint
when the listener is not loopback, and says so at startup:

```
WARN backup endpoint not mounted: the metrics listener is reachable from
     outside this machine addr=0.0.0.0:9090
```

Metrics still serve normally; only `/admin/backup` is withheld. To take
backups in that configuration, use `panmail backup` with the gateway stopped,
or expose metrics through a sidecar that scrapes over loopback.

Do not "fix" this by putting the listener back on loopback and adding a port
forward. The endpoint writes the database, every store, and `config.yaml` —
which carries the auth signing key — into a directory named in the query
string. Anything that can reach it can take all of that.
