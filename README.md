# Panmail Email Gateway

Panmail is a high-performance, lightweight email gateway built with Go and React. It acts as a professional middleware between your applications and standard email servers.

## 🏗️ The Concept

Panmail simplifies email communication by providing a unified interface for multiple email servers.

```text
Other Application -> [ Panmail Gateway ] -> Email Server (SMTP/IMAP/POP3)
```

By acting as a middleware, Panmail adds significant value to standard email protocols:
- **Unified API**: Send emails using a single gRPC/ConnectRPC interface regardless of the underlying server.
- **Visual Email Builder**: Drag-and-drop HTML builder with mobile/desktop previews and Outlook compatibility.
- **Advanced Templating**: Centralized Handlebars-based templates with variable injection.
- **Intelligent Routing**: Automatic failover across multiple SMTP servers and domain-based provider selection to prevent spoofing.
- **Persistent Buffering**: Integrated outbox with automatic retries for temporary delivery failures.
- **RBAC & Multi-Tenant**: Comprehensive multi-tenant support with role-based access control (Super Admin, Admin, Editor, Viewer).
- **Reputation Protection**: Global suppression lists to prevent sending to bounced or unsubscribed addresses.
- **Observability**: Unified analytics and logging for all your email traffic.

## 👥 User Roles (RBAC)

Panmail implements a strict 4-tier Role-Based Access Control system to ensure secure management:

- **Super Administrator**: Full system access. Manages tenants, assigns Administrators, and can switch between tenant contexts to monitor the entire gateway.
- **Administrator**: Tenant-level management. Manages Users, API Keys, and all email settings within their tenant.
- **Editor**: Configuration management. Manages Email Providers, Templates, Webhooks, and Suppressions. Can send test emails.
- **Viewer**: Read-only access to analytics, logs, and configurations. Cannot perform any modifications.

Deployment-wide settings — **Settings** (base URL, retention, content redaction) and the
**SMTP submission** listener — belong to the Super Administrator alone, because one change
there reaches every tenant. An Administrator sees the SMTP panel read-only and no Settings
page. An Administrator also cannot demote, delete or turn off two-factor for a Super
Administrator.

The **Logs** page is scoped the same way: a tenant's users see only the gateway log lines
that name their tenant, and only a Super Administrator sees the whole log.

A session's role is re-read from the account (cached for a few seconds) on every request, so
deleting, demoting or removing super admin from an account takes effect within seconds rather
than when the 24-hour session token expires.

For the product requirements behind all of this, see [docs/PRD.md](docs/PRD.md).

### One account, several tenants

A user does not need a separate login per tenant. A Super Administrator can assign an
existing account to another tenant from **Dashboard → Users → Tenant Access**, and the
person keeps one address and one password.

- Each user has a **home** tenant — the one they sign in to, and where their role above
  applies.
- Any further tenant is a **membership**, and carries its own role. An Administrator at
  home joins another tenant as a Viewer unless told otherwise, so lending an account never
  hands over authority by accident.
- Users belonging to more than one tenant get a switcher in the header. Everyone else
  sees no switcher, because there is nothing to switch to.
- **Super Administrator is global**, not per tenant: it already reaches every tenant, so
  it cannot be granted as a membership.

Revoking a membership removes access to that tenant only. The account itself lives in its
home tenant and can only be deleted from there.

## 🔐 Security

Panmail is designed with a "Security First" mindset:

- **Two-Factor Authentication (2FA)**: Users can enable TOTP-based 2FA (e.g., Google Authenticator) in their profile settings. Administrators can enforce 2FA for their team members.
- **Login Rate Limiting**: The system automatically blocks an account after 5 failed login attempts for 15 minutes to prevent brute-force attacks. The limit holds under concurrent bursts (an attempt is reserved before the password is checked) and under floods of invented addresses (a full table evicts idle entries, never a locked one).
- **Password Length**: Every account, including the first administrator created by the setup wizard, needs a password of at least 12 characters.
- **API Key Scoping**: API keys are tied to a single tenant and carry an explicit set of scopes (e.g. `email:send`, `providers:read`). A key only grants what it was issued for, and can be expired or revoked at any time.
- **Secret Storage**: API keys are stored only as SHA-256 hashes and are never recoverable. Provider credentials (SMTP/IMAP/POP3 passwords, webhook signing secrets) must be readable to be used, so they are encrypted at rest with AES-256-GCM and redacted from every API response. Set `PANMAIL_SECRET_KEY` to a 64-character hex key to keep the encryption key out of the config file; otherwise a key is generated at setup and stored in `~/.panmail/db_config.yaml`, which protects against a stolen database but not a stolen disk.
- **Signed Tracking Links**: Open and click URLs are HMAC-signed, so delivery events cannot be forged and the click endpoint cannot be used as an open redirect.
- **Verified Provider Webhooks**: Inbound delivery webhooks are verified (SendGrid ECDSA, Mailgun HMAC, or a shared HMAC secret) before any event is recorded.

## 🚀 Features

- **Multi-Tenant Support**: Support for multiple tenants, each with their own set of providers, templates, and custom retry patterns.
- **Visual Email Builder**: Professional drag-and-drop editor with multi-column support, mobile/desktop frames, Outlook compatibility, and dynamic merge tags.
- **Advanced Templating**: Dual-engine support for **Handlebars** and standard **Go `html/template`** syntax.
- **Built for Throughput**: Asynchronous outbox batching, pooled SMTP connections, and thread-safe caching. Actual throughput depends on your provider's rate limits and connection concurrency — benchmark against your own setup rather than relying on a headline figure.
- **Intelligent Delivery**: Automatic classification of **Soft vs. Hard bounces**, with custom retry patterns (e.g., `5m, 1h, 1d`) and reputation-protecting suppressions.
- **Unified Analytics**: Real-time visualization of delivery trends (Sent, Delivered, Opened, Clicked, Bounced) with historical archiving.
- **Security First**: Integrated **Two-Factor Authentication (TOTP)**, login rate limiting, and role-based access control (RBAC).
- **Outbound Webhooks**: Standardized HTTP hooks for delivery events and inbound emails.
- **Automated Maintenance**: Per-class data retention set from the UI — events, message content, archives, queues, logs and inbound mail — with automatic JSONL archiving of delivery events for long-term auditability.
- **Database Support**: PostgreSQL and SQLite, with automatic migrations. MySQL/MariaDB are not supported: the query layer uses PostgreSQL-style positional parameters, so a `mysql` or `mariadb` database type is refused at startup (and by the setup wizard) with an error naming PostgreSQL and SQLite.
- **Production Ready**: Structured logging, gRPC health checking (`/healthz`), and graceful shutdown.

## 🏗️ Architecture

Panmail follows a clean, layered architecture:
`Transports` → `Middlewares` → `Endpoints` → `Services` → `Usecases` → `Repositories`.

- **Backend**: Go 1.27+
- **Frontend**: React 19, TypeScript, Mantine v9, Vite, TanStack Query/Router
- **API**: ConnectRPC / gRPC
- **Database**: PostgreSQL or SQLite
- **Logs**: Pebble KV store for high-performance logging

## 🛠️ Getting Started

### Prerequisites

- Go 1.27.1 (the version `go.mod` pins)
- Bun (for frontend builds)
- Buf (for gRPC generation)
- PostgreSQL or SQLite

### Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/gsoultan/panmail.git
   cd panmail
   ```

2. Build the application (Automated build including UI):
   ```bash
   # Requires Bun and Go installed
   ./panmail build --built-ui
   ```

### Running

1. Run the application; it will automatically detect if it's the first run and guide you through the setup wizard.
2. Start the server:
   ```bash
   ./panmail
   ```
3. Access the dashboard at `http://localhost:8080`.

### Development

```bash
./scripts/dev.sh
```

Compiles the gateway, creates a SQLite database, performs first-run setup,
prints the generated admin password, and runs the API alongside the Vite dev
server with hot reload on both. Everything it creates stays in `.dev/`, so an
installed instance in `~/.panmail` is untouched.

See [docs/development.md](docs/development.md) for the database options,
`--single-port` mode, and regenerating protobuf artefacts.

### Health Checks

Two endpoints, asking different questions. Pointing the wrong probe at the
wrong one causes an outage rather than preventing one.

| Endpoint | Asks | Failing means |
| --- | --- | --- |
| `GET /healthz` | Is this process working? | Restart it |
| `GET /readyz` | Can it serve a request now? | Stop sending it traffic |

`/healthz` deliberately checks nothing external. A database failover fails every
instance's dependency check at once, and wiring that to liveness would restart
the whole fleet against a database already struggling.

`/readyz` pings the database and names what failed, without describing it — the
endpoint is public, so the body is the dependency name and nothing more:

```json
{"status":"not ready","failed":["database"]}
```

The answer is shared by concurrent probes and held for one second, so a flood of
probes costs one database round trip a second, and a failing dependency is
logged when it starts and stops failing rather than on every probe. From SIGTERM
on, `/readyz` answers `503 {"status":"shutting down"}` while the port stays open
for `--shutdown-delay` (default 5s), so a load balancer stops routing before
connections are refused. Set `--shutdown-delay=0` for local development.

A configured gateway that cannot reach its database at boot retries with backoff
for about two minutes and then exits non-zero, so its supervisor restarts it; a
failed migration exits at once rather than serving a half-migrated schema.

- **gRPC**: Implements the standard [gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md).

### Deploying

```bash
docker build -t panmail .
```

The image embeds the UI, runs unprivileged, and keeps the metrics listener on
loopback. `deploy/kubernetes/panmail.yaml` is a worked StatefulSet — a
StatefulSet rather than a Deployment because each replica needs its own Pebble
directories, and Pebble takes an exclusive lock on one.

`deploy/prometheus/alerts.yaml` covers the failures this gateway actually has,
all of which are quiet: a stalled outbox still returns 200 to every caller, a
worker that died looks like an idle one.

**[docs/scaling.md](docs/scaling.md) is the one to read before running more than
one instance.** It has the connection arithmetic — getting it wrong sends
duplicate mail rather than failing cleanly — plus what a deploy does to messages
in flight, and why you should not alert on RSS.

Configuration worth knowing:

```yaml
database:
  ssl_mode: verify-full   # defaults to prefer; the connection carries everything
  max_open_conns: 25      # instances × this + headroom ≤ server max_connections
app:
  base_url: https://...   # must be https, or one-click unsubscribe is omitted
```

### Webhooks (Incoming)

Panmail supports standardized webhooks to track delivery events. Configure your email providers to send events to:
`http://your-gateway:8080/webhooks/{tenant_id}/{provider_id}/{type}`

### Webhooks (Outbound)

Panmail can notify your external applications when email events occur or when new inbound emails are received.

1. **Configure Webhook**: Go to the **Webhooks** section in the Panmail dashboard.
2. **Add URL**: Provide your application's endpoint and select the events you want to subscribe to (e.g., Mail Delivered, Mail Inbound).
3. **Receive Events**: Panmail will send a POST request with a JSON payload to your endpoint:
   ```json
   {
     "event": "WEBHOOK_TRIGGER_EVENT_MAIL_SENT",
     "tenant_id": "your-tenant-id",
     "timestamp": 1712345678,
     "data": { ... }
   }
   ```

### Sending from another application

Panmail exposes the same ConnectRPC API the dashboard uses. It is **asynchronous**: a successful call means the gateway has written the message to its outbox and taken responsibility for delivering it, returning `PENDING` and a `message_id`. Delivery itself is reported afterwards through events and webhooks, all keyed by that id — so store it next to whatever prompted the send.

Two things to set up first:

1. **An API key.** Dashboard → **API Keys**. Give it the **`email:send`** scope; without it the send is refused with `permission_denied`. The key carries the tenant, so there is nothing else to configure.
2. **A provider id.** Dashboard → **Email Providers**. `provider_id` is **required** on every send: panmail will not guess which of a tenant's providers a message goes out through, because the wrong guess sends from the wrong domain.

Authenticate with the `X-API-Key` header — *not* `Authorization`, which carries a dashboard session and will reject a key as a malformed token.

#### With an SDK

The clients live in their own repo, **[gsoultan/panmail-sdk](https://github.com/gsoultan/panmail-sdk)**, in Go and PHP. They are the same small library each time: one call, typed refusals you can act on, and a deliberate refusal to retry anything whose outcome is unknown.

| Language | Install | Runtime deps |
| --- | --- | --- |
| Go | `go get github.com/gsoultan/panmail-sdk` | none — stdlib only |
| PHP | `composer require gsoultan/panmail-sdk` | ext-curl, ext-json |

There is no Node package: from Node, POST the JSON with `fetch` — the dashboard's **API Request** tab generates the call, and [`docs/WIRE.md`](https://github.com/gsoultan/panmail-sdk/blob/main/docs/WIRE.md) is the whole contract.

There is no Java client. One existed during development and was withdrawn before
the first tag: publishing it meant verifying a Maven Central namespace, which is
a release path none of the other three need. Java callers use the HTTP API or
the SMTP door below — both are shown, per language, under **Test Delivery** in
the dashboard.

```go
import panmail "github.com/gsoultan/panmail-sdk"

client, err := panmail.New("https://mail.example.com", os.Getenv("PANMAIL_API_KEY"))
if err != nil {
    return err
}

result, err := client.Send(ctx, panmail.Message{
    ProviderID: "0f8b8f4e-0000-4000-8000-000000000000",
    From:       "noreply@yourdomain.com",
    To:         []string{"recipient@example.com"},
    Subject:    "Hello from panmail",
    HTML:       "<h1>Welcome</h1>",
    Text:       "Welcome",
})
if err != nil {
    return err
}
log.Printf("queued as %s", result.MessageID)
```

None of the SDKs depends on this repo — they speak the wire protocol directly, which is written down in full in [`docs/WIRE.md`](https://github.com/gsoultan/panmail-sdk/blob/main/docs/WIRE.md). That document is also what you want if you would rather not take a dependency at all: it covers the JSON contract *and* the SMTP door below.

**No SDK retries a send whose outcome it does not know — unless the send carries an idempotency key.** Without one, retrying after a timeout is retrying a message that may already be on its way. The other safe case is a refusal, where the gateway says plainly that it did not accept the message — `WithRateLimitRetries(n)` and its equivalents turn that on.

> The in-repo `pkg/panmail` was the SDK's predecessor. It was deprecated when the clients moved out and is **removed** as of v1.4.0 — use `github.com/gsoultan/panmail-sdk`, which resolves today.

#### cURL

```bash
curl -X POST https://mail.example.com/panmail.v1.EmailService/SendEmail \
  -H "Content-Type: application/json" \
  -H "X-API-Key: pm_your_api_key_here" \
  -d '{
    "provider_id": "0f8b8f4e-0000-4000-8000-000000000000",
    "from": "sender@yourdomain.com",
    "to": ["recipient@example.com"],
    "subject": "Hello from Panmail",
    "body_html": "<h1>Welcome</h1><p>Sent via Panmail Gateway</p>",
    "body_text": "Welcome! Sent via Panmail Gateway"
  }'
```

With a stored Handlebars template instead of a body:

```bash
curl -X POST https://mail.example.com/panmail.v1.EmailService/SendEmail \
  -H "Content-Type: application/json" \
  -H "X-API-Key: pm_your_api_key_here" \
  -d '{
    "provider_id": "0f8b8f4e-0000-4000-8000-000000000000",
    "from": "support@yourdomain.com",
    "to": ["user@example.com"],
    "template_id": "welcome-template-uuid",
    "template_data": {
      "name": "John Doe",
      "company": "Acme Inc",
      "verification_link": "https://your-app.com/verify?token=123"
    }
  }'
```

#### Retrying safely: idempotency keys

Set `idempotency_key` in the request (or send it as an `Idempotency-Key` header) and a send becomes safe to repeat:

```bash
curl -X POST https://mail.example.com/panmail.v1.EmailService/SendEmail \
  -H "Content-Type: application/json" \
  -H "X-API-Key: pm_your_api_key_here" \
  -H "Idempotency-Key: order-1042-receipt" \
  -d '{ "provider_id": "...", "from": "...", "to": ["..."], "subject": "...", "body_text": "..." }'
```

- A repeat **from the same tenant** with the same key within **24 hours** of the first being accepted gets the first response back — the same `message_id`, status `PENDING` — and nothing new is queued. It is not charged against the send rate.
- The same key with a **different request** is refused with `already_exists` ("idempotency key reused with a different request"). The request is compared by a hash of everything in it except the key.
- Two requests with one key **at the same moment** queue once: a unique constraint decides, and the loser is answered with the winner's message. If the winner's outcome cannot be read yet the loser gets `aborted`, which is safe to retry.
- Only **accepted** sends are remembered. A refusal — rate limit, suppression, invalid request — stores nothing, so retrying after one is decided afresh.
- A key is at most 255 printable ASCII characters. If both the field and the header are set they must match; otherwise `invalid_argument`.

#### What the refusals mean

| Response | Meaning | What to do |
| :--- | :--- | :--- |
| `resource_exhausted` **with** `Retry-After` | Over the tenant's configured send rate. Not accepted. | Wait the stated delay and send again. Safe to repeat. |
| `resource_exhausted` **without** `Retry-After` | The tenant's queue is already deeper than its rate can drain. Not accepted. | Stop sending. There is no delay to wait out, and retrying makes the wait longer for everything queued. |
| `failed_precondition` | A recipient is on the tenant's suppression list. The **whole** message is refused, not just that recipient's copy. | Remove the address from the send, or lift the suppression. Retrying changes nothing until one of those happens. |
| `invalid_argument` | The provider or template named does not exist, the template will not render against the data sent, or the idempotency key is unusable. | Fix the request. Retrying never helps. |
| `already_exists` | The idempotency key was already used, within 24 hours, for a different request. Not accepted. | Use a new key, or resend exactly what was sent the first time. |
| `aborted` | A concurrent request with the same idempotency key is still being decided. Not accepted. | Retry with the same key; it will return that request's message. |
| `unauthenticated` | Key missing, unknown or revoked. | Fix the key. Retrying never helps. |
| `permission_denied` | The key lacks the `email:send` scope. | Add the scope. |
| `unknown` | Something failed rather than refused — storage, a provider connection. | Retry with backoff. |

The split that matters is between a refusal and a failure. A refusal is a decision: the same request is answered the same way until somebody changes something, so retrying it spends attempts on an answer that cannot move. Only `unknown` means the gateway might succeed if asked again.

The presence of `Retry-After` is what separates the two capacity refusals — they deliberately share a status code, because they are the same answer to the caller: you are asking for more than you may have. The Go client turns them into `panmail.RateLimitedError` and `panmail.BacklogFullError` so you do not have to read headers.

### Sending over SMTP

For an application that already speaks SMTP — an existing framework mailer, or
anything you would rather not fit a new client into — panmail accepts
submissions directly. It is the same pipeline: a message submitted over SMTP
goes through the same send usecase as the RPC API, so the rate limit, backlog
ceiling, suppression list and per-provider `AllowedDomains` anti-spoofing
checks all apply unchanged.

The listener is **off by default**, and there are two ways to turn it on.

**From the dashboard**, under **API Keys → SMTP submission → Configure**. An
administrator picks whether the port is reachable only from this machine or
from the network, sets the port, and pastes a TLS certificate and key. The key
is encrypted with the gateway's data key before it is stored and is never
readable back — not through the API and not in the panel, which shows the
certificate's subject, expiry and SHA-256 fingerprint instead.

Two rules are enforced by the server and are stricter here than for the flags
below, because a flag is set by someone with shell access and a form is
submitted by anyone holding an admin session:

- Sign-in without TLS is **refused** on any listener reachable from another
  host, rather than warned about. The SMTP password is an API key.
- Only a signed-in administrator may change it. API keys are refused outright,
  so a key cannot widen the surface on which its own kind is accepted.

The change takes effect immediately, without a restart, and reaches every
gateway sharing the database within one reconcile interval — thirty seconds by
default, and `--smtp-reconcile-interval` if a large fleet would rather trade
propagation delay for fewer settings reads. The same pass revives a listener
that died on its own, so the interval is also the worst case for that. If the port cannot be
bound — something else is already on it, or it is privileged and this process
lacks the capability — the previous listener is left running and the panel says
why.

**From process flags**, which win over the stored configuration: a gateway
started with `--smtp-addr` ignores the stored row entirely and the panel renders
read-only. This is the original path and is unchanged, including being fatal at
startup when the listener will not build.

Enable it with `--smtp-addr`:

```bash
panmail --smtp-addr :587 \
        --smtp-tls-cert /etc/panmail/tls.crt \
        --smtp-tls-key  /etc/panmail/tls.key
```

The dashboard shows the live connection details under **API Keys → SMTP
submission**, including a per-provider username you can copy. It reads them
from the running process, so if it says the listener is off, it is off.

Then point the application at it:

| Setting | Value |
| :--- | :--- |
| Host / Port | the address you passed to `--smtp-addr` |
| Encryption | STARTTLS |
| Username | the **provider id** (a UUID) |
| Password | the **API key**, with the `email:send` scope |

The username carries the provider id because SMTP has no field for it and
`provider_id` is required on every send. A message may override it with an
`X-Panmail-Provider-Id` header, so one connection can send through more than
one provider; the header is stripped before the message goes out.

**TLS is required for AUTH.** The password is an API key, which is a tenant's
entire sending authority, so the server refuses to start without either a
keypair or an explicit `--smtp-allow-insecure-auth`. Use that flag only where
the hop is already private, such as a container network or loopback.

Storing a certificate through the dashboard requires a data encryption key
(`PANMAIL_SECRET_KEY`, the same one that protects provider credentials). Without
one the panel is read-only and says so: a TLS private key is never written in
the clear as a fallback.

#### What the reply codes mean

| Code | Meaning | What the client does |
| :--- | :--- | :--- |
| `250` | Queued. The message is in the outbox and panmail owns delivery from here. | Done. Delivery is reported through events and webhooks. |
| `451 4.7.1` | Over the tenant's send rate. **Not** queued. | Retries. The delay to wait is in the reply text. |
| `452 4.3.1` | The queue is deeper than the rate can drain. **Not** queued. | Retries later. |
| `535 5.7.8` | The key is unknown, disabled, expired, or lacks `email:send`. | Gives up. Fix the key. |
| `501 5.5.4` | No provider id, or one that is not a UUID. | Gives up. Fix the username or the header. |
| `451 4.3.0` | Something else went wrong. **Not** queued. | Retries. |

Every failure code above is returned *before* the message is written to the
outbox, so a client that retries one cannot produce a duplicate send. The `250`
is written only after the message is queued.

If the connection drops in the moment between panmail queueing a message and
the `250` reaching the client, the client will resubmit. A submission that
carries a `Message-ID` header is de-duplicated: its Message-ID together with
the envelope recipients is the send's [idempotency key](#retrying-safely-idempotency-keys),
so a resubmission within 24 hours gets a `250` for the message already queued
rather than a second copy. A relay that splits one message into several
transactions for different recipients still has each of them queued. The same
Message-ID to the same recipients with *different* content is refused with
`550 5.7.1`. A submission without a Message-ID cannot be de-duplicated, and a
resubmission of it is sent twice.

### 🧬 Advanced Templating

Panmail supports two template engines: **Handlebars** (default) and standard **Go `html/template`**. This allows you to use the syntax you are most comfortable with.

#### Handlebars vs. Go Syntax
- **Handlebars**: `Hello {{name}}!` or `{{#each items}}...{{/each}}`
- **Go Templates**: `Hello {{.Name}}!` or `{{range .Items}}...{{/range}}`

#### Iterating over Arrays (Loops)
To render a list or table dynamically, pass an array in `template_data`:

```json
{
  "template_data": {
    "items": [
      { "name": "Widget A", "price": "$10" },
      { "name": "Widget B", "price": "$20" }
    ]
  }
}
```

In your template:
```html
<ul>
  {{#each items}}
    <li>{{name}}: {{price}}</li>
  {{/each}}
</ul>
```

In the **Visual Builder**, you can enable the "Loop Variable" setting on Table or List components and set it to `items`. The first row or item will be used as a template for each element in the array.

#### Accessing Maps (Nested Objects)
You can access nested properties using dot notation:

```json
{
  "template_data": {
    "user": {
      "profile": {
        "first_name": "Jane"
      }
    }
  }
}
```

In your template:
`Hello {{user.profile.first_name}}!`

#### Conditional Logic
```html
{{#if is_admin}}
  <p>Welcome, Administrator!</p>
{{else}}
  <p>Welcome, User!</p>
{{/if}}
```

#### Formatting Dates

A timestamp in `template_data` is a string — JSON has no date type — so call
`.Format` on it to choose how it reads:

```json
{ "template_data": { "created_at": "2026-12-01T09:30:00Z" } }
```

```html
Ordered {{ created_at.Format "2026-12-01" }}      <!-- 2026-12-01 -->
Ordered {{ created_at.Format "DD/MM/YYYY" }}      <!-- 01/12/2026 -->
Ordered {{ created_at.Format "MMMM D, YYYY" }}    <!-- December 1, 2026 -->
Ordered {{ created_at.Format "long" }}            <!-- December 1, 2026 -->
Ordered {{ .created_at.Format "2006-01-02" }}     <!-- 2026-12-01 -->
```

Write the layout whichever way you already know:

| Style | Example | Renders |
| :--- | :--- | :--- |
| **An example date** | `"2026-12-01"`, `"December 1, 2026"`, `"12/01/2026"` | the same shape, with the real date |
| **Tokens** | `YYYY` `MM` `DD` `HH` `mm` `ss` `MMM` `MMMM` `DDD` `hh` `A` | `"DD MMM YYYY HH:mm"` → `01 Dec 2026 09:30` |
| **Named** | `date` `time` `datetime` `iso` `rfc3339` `rfc1123` `kitchen` `short` `long` `human` | `"human"` → `Dec 1, 2026 9:30 AM` |
| **Go's own layout** | `"2006-01-02"`, `"Jan 2, 2006 3:04 PM"` | unchanged, so existing templates keep working |

Both spellings of the call work — `{{ created_at.Format "…" }}` as Handlebars
writes a field, and `{{ .created_at.Format "…" }}` as Go does — and the layout
may be in single or double quotes.

> **Why an example date works.** Go's own layout *is* a date: `2006-01-02` means
> year-month-day because 2006 is the reference year. Write that shape with any
> other date and stock Go does not complain — it renders `1016-121-12`. Panmail
> reads an example as an example instead, because a silently wrong date in a
> receipt or an expiry notice is the worst thing an email can contain.

Panmail reads timestamps as RFC 3339 (`2026-12-01T09:30:00Z`), as
`2026-12-01 09:30:00`, as `2026-12-01`, and as a Unix time in seconds or
milliseconds. A field that holds none of these fails the send with an error
naming it, rather than sending mail with the date missing.

#### Time Zones

A timestamp is an instant, but a recipient reads a wall clock — so an event mail
has to name the hour in *their* zone. Call `.In` with an IANA zone name:

```html
{{ .StartAt.In "Asia/Jakarta" }}
{{ (.StartAt.In "Asia/Jakarta").Format "DD MMM YYYY, HH:mm" }}
{{ .StartAt.Format "DD MMM YYYY, HH:mm" "Asia/Jakarta" }}
{{ .StartAt.In (time.LoadLocation "Asia/Jakarta") }}
```

For `2026-12-01T09:30:00Z` those render:

```
2026-12-01 16:30:00 +0700 WIB
01 Dec 2026, 16:30
01 Dec 2026, 16:30
2026-12-01 16:30:00 +0700 WIB
```

The last form is Go's own longhand, accepted so that a template written as Go
code works unchanged. Note that a zone can move the **date** as well as the
hour: 02:00 UTC on 1 December is still 30 November in Los Angeles.

The zone database is embedded in the binary, so a zone resolves the same in
development and in production. A name panmail does not know fails the send and
says which name it was, rather than quietly falling back to UTC and showing an
hour nobody chose.

#### Example: Send with Attachments

Panmail allows you to include attachments in your emails. Attachments should be base64 encoded.

```bash
curl -X POST http://localhost:8080/panmail.v1.EmailService/SendEmail \
  -H "Content-Type: application/json" \
  -H "X-API-Key: pm_your_api_key_here" \
  -d '{
    "from": "billing@yourdomain.com",
    "to": ["customer@example.com"],
    "subject": "Your Invoice",
    "body_html": "<h1>Invoice Attached</h1><p>Please find your invoice for this month attached.</p>",
    "attachments": [
      {
        "filename": "invoice.pdf",
        "content_type": "application/pdf",
        "content": "JVBERi0xLjQKJ..."
      }
    ]
  }'
```

#### Example: Send with Template and Attachments

You can combine templates and attachments in a single request.

```bash
curl -X POST http://localhost:8080/panmail.v1.EmailService/SendEmail \
  -H "Content-Type: application/json" \
  -H "X-API-Key: pm_your_api_key_here" \
  -d '{
    "from": "billing@yourdomain.com",
    "to": ["customer@example.com"],
    "template_id": "invoice-notification-uuid",
    "template_data": {
      "customer_name": "Jane Smith",
      "amount": "99.00"
    },
    "attachments": [
      {
        "filename": "receipt_123.pdf",
        "content_type": "application/pdf",
        "content": "JVBERi0xLjQKJ..."
      }
    ]
  }'
```

Panmail also supports gRPC for high-performance integrations.

### 📈 Performance & Reliability

- **Throughput**: Delivery runs up to 200 sends at once per instance and is bound by the provider's round-trip time, not CPU — a sustained soak measured about 190 sends/s on one instance. Benchmark against your own provider rather than relying on a headline figure.
- **Large recipient lists**: Each recipient's send has its own timeout, and a pass that delivers to anyone does not use up a retry, so a long list is not cut short by a per-message deadline.
- **Leased delivery**: An instance confirms and renews its claim on a message while sending it, so a slow batch never lets a second instance send the same row.
- **Intelligent Retries**:
    - **Soft Bounce**: Temporary failures (e.g., mailbox full, rate limited, greylisting, any `4xx` reply) are retried using a tenant-specific backoff pattern.
    - **Hard Bounce**: Permanent failures that name the recipient (e.g., `550 5.1.1 user unknown`) are immediately suppressed to protect your sender reputation.
    - **Not the recipient's fault**: An authentication or TLS failure, a blocklisting, a network error or a content filter's refusal (`554 5.7.1 message rejected as spam`) never suppresses anyone. The classifier reads only the server's reply — never the recipient's address or the provider's IP in front of it — and matches status codes whole.
- **Automated Archiving**: To maintain performance, delivery logs are truncated after a retention period (default 14 days) and archived into compressed JSONL files in the `archives/` directory.

#### Data retention

Every class of data panmail stores has its own retention, set in **Settings → Data Retention** or in the config file. Retention is in whole days and **zero means keep forever**; a change saved in the UI applies immediately rather than at the next daily pass.

| Setting | Data | Default | On expiry |
| :--- | :--- | :--- | :--- |
| `log_retention_days` | Delivery events | 14 days | Archived to JSONL, then removed |
| `message_retention_days` | Subjects, bodies, attachments | forever | Deleted outright |
| `archive_retention_days` | The JSONL archives above | forever | Deleted outright |
| `outbox_retention_days` | Permanently failed sends | forever | Deleted outright |
| `webhook_retention_days` | Finished notifications | 7 days | Deleted outright |
| `app_log_retention_days` | The gateway's own logs | forever | Deleted outright |
| `inbound_retention_days` | Received mail | forever | Deleted outright |
| `quarantine_retention_days` | How long a held message waits for review, and how long finished review records are kept | forever | Held payload discarded; review record deleted |

Two kinds of telemetry are pruned on a fixed horizon rather than a setting: per-minute analytics counters after 30 days and resource samples after 7 days. So are the inbound endpoint's receipts of posts already accepted (hashes, not mail), after 7 days. Hour and day counters, which the analytics pages read for anything longer, are kept. Analytics buckets are in UTC; a server that was not running in UTC sees its graphs shift once by its offset at the upgrade that introduced this.

When events are written to both the local store and PostgreSQL (`-shared-events`, or the shadow phase), retention prunes both, and archives once.

Only the two defaults that panmail has always enforced are non-zero, so upgrading does not start deleting anything. Message content is the one worth setting deliberately: bodies and attachments are never archived, and until you give them a retention they outlive the delivery events that explain them.

### Inbound Processing

Receive and parse incoming emails by configuring Inbound Parse in your provider and pointing it to your gateway's inbound endpoint:

```
POST https://<your-gateway>/inbound/{tenant_id}/{provider_id}
```

The endpoint is authenticated with that provider's **webhook secret** (the same field that verifies its delivery-event webhooks, set on the provider's page). Present it in either of two ways:

- **HMAC signature** — `X-Panmail-Signature: sha256=<hex HMAC-SHA256 of the raw request body, keyed with the secret>`, for a relay you control.
- **HTTP Basic** — for a service that can only be given a URL: `https://:<secret>@<your-gateway>/inbound/...`, or `https://<user>:<pass>@...` when the secret is stored as `user:pass`.

The tenant is the one the provider belongs to; a `tenant_id` in the body is ignored. A post without valid credentials, to an unknown provider, or to a provider with no secret is refused with `401`.

Two provider types store something other than a shared secret, and authenticate differently:

- **SendGrid.** The webhook secret holds the event webhook's public key, which is public and so cannot be a credential. Add the Inbound Parse credential to the same field after a space — `<public key> inbound=<user>:<pass>` (or `inbound=<pass>`), or just `inbound=<user>:<pass>` if you do not use the event webhook — and give Inbound Parse the URL `https://<user>:<pass>@<your-gateway>/inbound/{tenant_id}/{provider_id}`. Only HTTP Basic is accepted for SendGrid.
- **SES.** Create a receipt rule with an **SNS action** publishing to a topic, and subscribe `https://<your-gateway>/inbound/{tenant_id}/{provider_id}` to that topic over HTTPS, with raw message delivery off. Add the topic's ARN to the provider's webhook secret (comma-separated alongside the delivery-events topic, if they differ). Each post is verified by its SNS signature (SignatureVersion 1 or 2), and only from a topic listed there; the signing certificate is fetched only from `https://sns.<topic's region>.amazonaws.com` and cached, and redirects are not followed. The subscription confirmation is followed only for a listed topic and only to that same endpoint — panmail confirms it itself, so mail starts arriving once the ARN is saved and the subscription is created. Limits: the SNS action carries messages up to 150 KB, inline; mail delivered by an **S3 action** arrives with its headers only and is stored with `X-Panmail-Ses-S3-Bucket` and `X-Panmail-Ses-S3-Object-Key` headers saying where the rest is — panmail does not read from S3. A notification that is not received mail (a bounce sent to the wrong topic) is refused with `400`, and one whose certificate AWS could not serve gets `503` so SNS retries.

The body may be JSON (`from`, `to` as an array, `subject`, `text`, `html`, `headers` as an object, `raw` as the whole message), `multipart/form-data` or `application/x-www-form-urlencoded` (`from`, `to` comma-separated, `subject`, `text`, `html`, `headers` as a raw header block, `email` as the whole message — SendGrid's "send raw"). A post that yields no message is refused with `400`, and an unsupported `Content-Type` with `415`.

A received message is recorded as a bounce only when it is a real delivery status notification (a `multipart/report` with a `message/delivery-status` part) **and** the original headers it returns carry the `X-Panmail-Ref` header panmail put on that copy. The reference is signed with the tracking key over the tenant, the message and the one recipient, so it must verify for the tenant whose mailbox received the report and for the `Final-Recipient` (or `Original-Recipient`) the report names, and the event store must show that message was sent to that address. Anything else — a plain "user unknown" message, a report naming an address panmail never mailed, a report about another tenant's message — is stored as mail and suppresses no one. Over HTTP, only a post that includes the raw message can carry a report.

### UI Integration

The frontend is integrated into the Go binary using Go embedding. By default, `make build` includes the UI by passing the `builtui` tag to the Go compiler. If you wish to build the backend without the UI, you can run:
```bash
rtk go build -o panmail ./cmd/api
```
The application will detect that the UI is not embedded and log a message accordingly.

## 🧪 Testing

```bash
make check
```

Runs the gates CI enforces, in the order that fails cheapest: `gofmt`, `go build`,
`go vet`, `go test -race`, then the frontend's lint, test and build. `make fmt` fixes
whatever the formatting step reports.

Formatting is a CI step of its own and it runs *after* the tests, so a green
`go test` on its own does not mean a green build.

One side at a time:

```bash
make check-backend
make check-frontend
```

Two things stay in CI and are deliberately not in `make check`: the second test pass
against PostgreSQL, which needs a server (`scripts/ci/services.sh up` starts one and
exports `PANMAIL_TEST_POSTGRES`), and `govulncheck`, which needs the network.

## ⬆️ Upgrading

Changes that need attention when you upgrade (inbound URL and secret,
Super Administrator-only settings and metrics, 12-character passwords, the
database at boot, `--shutdown-delay` and more) are listed per release in
[docs/upgrading.md](docs/upgrading.md). Read every section between the version
you run and the one you are moving to.

## 📜 License

MIT
