# Soak

Measures what the gateway actually does under load, rather than what its unit
tests say it should. Two pieces, both deliberately small:

- `sink.py` — an SMTP server that speaks just enough of RFC 5321 to accept a
  message and count it. Not a mail server. Using a real provider would measure
  the provider, and the question here is about panmail's queue.
- `drive.py` — fires N sends at the API concurrently and reports what the API
  answered.

## Running it

```bash
python3 scripts/soak/sink.py 2599 &          # the provider points here
./scripts/dev.sh backend --port 8090 --no-watch &

TOKEN=$(curl -s -X POST http://localhost:8090/panmail.v1.AuthService/SignIn \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@panmail.local","password":"..."}' | jq -r .token)

python3 scripts/soak/drive.py http://localhost:8090 "$TOKEN" <provider-id> 2000 20
```

Then watch it drain, which is the actual measurement:

```bash
curl -s http://127.0.0.1:9090/metrics | grep panmail_outbox
```

## What to look at

`panmail_outbox_pending` should rise to the batch size and fall to zero.
`panmail_outbox_oldest_seconds` climbing in step with wall-clock time is the
signature of a queue that is not draining at all — during the first run of this
it sat at 2000 pending with the age tracking the clock exactly, which is what a
stalled worker looks like. (That time the cause was the sink refusing AUTH, but
the shape is the same whatever the cause.)

## Measured on a laptop, 2026-08-14

Against the local sink, SQLite, one gateway process:

| | |
|---|---|
| Admission | ~3,000/second, all 200 |
| Delivery | **193/second** sustained |
| 2,100 messages | drained in 10.9s |
| SQLITE_BUSY | 0 |
| Rows left behind | 0 |

Delivery against a real provider will be slower — network round-trips dominate,
not this code. Treat 193/s as the ceiling this imposes, not the throughput you
will see.
