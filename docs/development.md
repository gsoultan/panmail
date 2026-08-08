# Running Panmail in development

```bash
./scripts/dev.sh
```

That is the whole thing. On the first run it compiles the gateway, creates a
SQLite database, performs the setup wizard for you, creates an admin user and
prints its password, then starts the Go gateway and the Vite dev server
together. Open the URL it prints and sign in.

`./scripts/dev.sh --help` lists every command and option.

## What it creates

Everything lives in `.dev/` at the repository root, and nothing else is
touched:

```text
.dev/
├── dev.env                    generated secrets and pinned settings (0600)
├── config/db_config.yaml      the gateway's config: PASETO key, DB details (0600)
├── state/panmail.sqlite       the development database
├── state/{logs,events,inbound}.db   Pebble stores
└── bin/panmail-dev            the compiled gateway
```

`.dev/` is gitignored. Deleting it, or running `./scripts/dev.sh reset`, returns
you to a clean first run.

A real installation keeps its signing key and database password in
`~/.panmail/db_config.yaml`. The development scripts pass `--config` so they
never read, overwrite or lock that file — you can run a development instance
alongside an installed one.

## The two serving modes

By default the UI and the API are served separately:

| | serves | port |
| :--- | :--- | :--- |
| Vite | the React app, with HMR | 5173 |
| Go gateway | the API | 8080 |

The ConnectRPC client uses an empty `baseUrl` (`web/src/services/client.ts`), so
every call goes to whichever origin served the page. In production that is the
gateway. In development it is Vite, which forwards the gateway's routes — the
`panmail.v1.*` procedures, `/healthz`, `/webhooks/`, `/inbound/` and `/track/` —
to the Go process. That proxy is the `server.proxy` block in
`web/vite.config.ts`; without it the dev server cannot reach the API at all.

Adding a new ConnectRPC service needs no change there: procedures are matched by
their `panmail.v1.` package prefix. A new *non*-RPC HTTP route does, or Vite will
answer it with the SPA.

`./scripts/dev.sh --single-port` runs the other way: the gateway serves
`web/dist` itself on 8080, exactly as production does. No HMR, so a UI change
means a rebuild — but it is the way to check the `/setup` redirect and the SPA
fallback under the real routing.

## Choosing a database

SQLite is the default because it needs nothing installed:

```bash
./scripts/dev.sh                 # sqlite
./scripts/dev.sh --db postgres   # postgres in a container on port 5433
```

PostgreSQL is what production runs, and it is the only engine where the whole
API works. Email **provider name search** is built with `ILIKE`
(`internal/email_provider/repositories/stores/postgres/store.go`), which SQLite
does not implement — that one query fails on SQLite. Use `--db postgres` when
you are working on provider search or anything else engine-specific.

MySQL and MariaDB are not usable: the embedded queries use `$1` positional
parameters, which those drivers reject. Setup succeeds and then every query
fails.

The container is managed for you: Apple's [`container`](https://github.com/apple/container)
on macOS 26+, no Docker required. `docker` and `podman` are accepted as
fallbacks so the same scripts work on Linux. The container is named
`panmail-dev-postgres`, the volume `panmail-dev-postgres-data`, and the port is
published on `127.0.0.1:5433` so it cannot collide with a PostgreSQL you already
run on 5432.

Two things differ from Docker and are handled for you:

- `container inspect` has no `--format`, so container state is read out of
  `container ls --format json` with `jq` (which ships with macOS 26).
- Apple's volumes are formatted block devices, so a fresh one already contains a
  `lost+found` and `initdb` refuses it — *directory "/var/lib/postgresql/data"
  exists but is not empty*. `PGDATA` therefore points at a subdirectory of the
  mount. That is harmless on Docker, so it is done unconditionally.

To use a server you run yourself:

```bash
PANMAIL_DEV_PG_EXTERNAL=1 PANMAIL_DEV_PG_HOST=… PANMAIL_DEV_PG_PORT=… \
  ./scripts/dev.sh --db postgres
```

Switching engines needs a fresh setup, because the engine is recorded in the
config file: `./scripts/dev.sh --db postgres --fresh`.

## Hot reload

The gateway is rebuilt and restarted when a `.go` or `.sql` file under `cmd/`,
`internal/`, `pkg/` or `api/` changes. Test files are ignored so that editing a
test does not bounce the server. If a build fails the previous binary keeps
serving and the error is printed — you are never left without a server because
of a typo mid-edit.

If [`air`](https://github.com/air-verse/air) is installed it is used instead; it
debounces better. Its config is generated into `.dev/air.toml`, so there is
nothing to keep in sync.

`--no-watch` turns the watcher off.

## After changing a `.proto`

```bash
./scripts/dev.sh generate
```

One `buf generate` produces both halves of the contract — the Go code under
`api/` and the TypeScript client under `web/src/api` (see `buf.gen.yaml`).
Regenerating only one leaves the two sides disagreeing in ways that surface at
runtime rather than at compile time. The command lints first and then checks
that the generated Go still compiles.

## Commands

| | |
| :--- | :--- |
| `./scripts/dev.sh` | start everything (`up`) |
| `./scripts/dev.sh backend` | the gateway alone |
| `./scripts/dev.sh frontend` | Vite alone |
| `./scripts/dev.sh status` | ports, database, admin credentials |
| `./scripts/dev.sh preflight` | check the toolchain and ports |
| `./scripts/dev.sh generate` | regenerate protobuf artefacts |
| `./scripts/dev.sh db up\|down\|reset\|status` | the development database |
| `./scripts/dev.sh stop` | stop the database container |
| `./scripts/dev.sh reset` | delete `.dev/` and start over |

There are `make dev*` wrappers for the common ones (`make dev`, `make dev-backend`,
`make dev-reset`, …). The scripts call `go`, `bun` and `buf` directly rather than
through `rtk`, so a compiler error reaches you unfiltered.

## Settings

Read from `.dev/dev.env`; anything already exported wins, so a one-off override
works without editing the file.

| | |
| :--- | :--- |
| `PANMAIL_DEV_DB` | `sqlite` or `postgres` |
| `PANMAIL_DEV_PORT` | gateway port (default 8080) |
| `PANMAIL_DEV_WEB_PORT` | Vite port (default 5173) |
| `PANMAIL_DEV_ADMIN_EMAIL` | admin created at bootstrap |
| `PANMAIL_DEV_ADMIN_PASSWORD` | pin one instead of having it generated |
| `PANMAIL_SECRET_KEY` | 64 hex chars; encrypts stored provider credentials |
| `PANMAIL_DEV_PG_*` | `HOST`, `PORT`, `USER`, `PASSWORD`, `DB`, `EXTERNAL`, `CONTAINER`, `IMAGE` |

Two of these are worth understanding rather than just setting.

**`PANMAIL_SECRET_KEY`** is generated once into `.dev/dev.env` and exported for
every run. Provider passwords and webhook secrets in the database are encrypted
with it, so replacing it makes the existing ones permanently unreadable. Keeping
it in the environment rather than the config file is also what `README.md`
recommends for production, so development exercises the same code path
(`config.ResolveDataKey` prefers the environment variable).

**The admin password** is generated, not defaulted to something well known. The
gateway binds every interface — `cmd/api/main.go` listens on `":$PORT"`, not
`127.0.0.1` — so on a shared or untrusted network a predictable development
password is a real account on a real admin API. `./scripts/dev.sh status`
reprints it.

## Why bootstrap works the way it does

`IsSetup` decides the instance is configured purely by whether the config file
exists (`internal/setup/usecases/setup_usecase.go`). Writing that file by hand
would therefore satisfy `IsSetup`, make `Setup()` refuse to run, and leave you
with an instance that has no admin user and no way to create one.

So `scripts/dev/bootstrap.sh` does not write the config. It starts the gateway
on a scratch port, calls the `SetupService.Setup` RPC — which `policy.go` marks
`public: true` precisely so it can be reached before any credential exists — and
lets the application write its own config, generate its own PASETO key and
create the admin user through the same path the browser wizard uses.

It then stops that gateway before the real one starts. That restart is required,
not tidiness: the credential keyring is built once at start-up and only when a
config was already present, so the process that performed the setup is running
without one.

To go through the wizard in the browser instead, delete the config and start the
gateway on its own:

```bash
rm .dev/config/db_config.yaml
./scripts/dev.sh backend --single-port   # then open http://localhost:8080
```
