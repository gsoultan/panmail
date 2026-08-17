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

## 🔐 Security

Panmail is designed with a "Security First" mindset:

- **Two-Factor Authentication (2FA)**: Users can enable TOTP-based 2FA (e.g., Google Authenticator) in their profile settings. Administrators can enforce 2FA for their team members.
- **Login Rate Limiting**: The system automatically blocks an account after 5 failed login attempts for 15 minutes to prevent brute-force attacks.
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
- **Database Support**: PostgreSQL and SQLite, with automatic migrations. MySQL/MariaDB are not currently supported: the query layer uses PostgreSQL-style positional parameters, so those drivers connect but every query fails.
- **Production Ready**: Structured logging, gRPC health checking (`/healthz`), and graceful shutdown.

## 🏗️ Architecture

Panmail follows a clean, layered architecture:
`Transports` → `Middlewares` → `Endpoints` → `Services` → `Usecases` → `Repositories`.

- **Backend**: Go 1.26+
- **Frontend**: React 19, TypeScript, Mantine v9, Vite, TanStack Query/Router
- **API**: ConnectRPC / gRPC
- **Database**: PostgreSQL or SQLite
- **Logs**: Pebble KV store for high-performance logging

## 🛠️ Getting Started

### Prerequisites

- Go 1.26.3
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

#### Go

```go
import "github.com/gsoultan/panmail/pkg/panmail"

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

`go get github.com/gsoultan/panmail` brings it in. Only the packages you import are compiled, so this pulls in the generated API and ConnectRPC — not the gateway's database or storage engines. Templates, attachments and the error handling below are covered by the runnable examples in `pkg/panmail/example_test.go`.

**The client does not retry a send whose outcome it does not know.** Sending is not idempotent and there is no de-duplication key, so retrying after a timeout is retrying a message that may already be on its way. The one safe case is a refusal, where the gateway says plainly that it did not accept the message — `panmail.WithRateLimitRetries(n)` turns that on.

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

#### What the refusals mean

| Response | Meaning | What to do |
| :--- | :--- | :--- |
| `resource_exhausted` **with** `Retry-After` | Over the tenant's configured send rate. Not accepted. | Wait the stated delay and send again. Safe to repeat. |
| `resource_exhausted` **without** `Retry-After` | The tenant's queue is already deeper than its rate can drain. Not accepted. | Stop sending. There is no delay to wait out, and retrying makes the wait longer for everything queued. |
| `unauthenticated` | Key missing, unknown or revoked. | Fix the key. Retrying never helps. |
| `permission_denied` | The key lacks the `email:send` scope. | Add the scope. |

The presence of `Retry-After` is what separates the two capacity refusals — they deliberately share a status code, because they are the same answer to the caller: you are asking for more than you may have. The Go client turns them into `panmail.RateLimitedError` and `panmail.BacklogFullError` so you do not have to read headers.

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

- **High Throughput**: Capable of handling over 1000 messages per second using parallel background workers and asynchronous batch writing.
- **Intelligent Retries**:
    - **Soft Bounce**: Temporary failures (e.g., mailbox full, rate limited) are retried using a tenant-specific backoff pattern.
    - **Hard Bounce**: Permanent failures (e.g., invalid address) are immediately suppressed to protect your sender reputation.
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

Only the two defaults that panmail has always enforced are non-zero, so upgrading does not start deleting anything. Message content is the one worth setting deliberately: bodies and attachments are never archived, and until you give them a retention they outlive the delivery events that explain them.

### Inbound Processing

Receive and parse incoming emails by configuring Inbound Parse in your provider and pointing it to your gateway's inbound endpoint.

### UI Integration

The frontend is integrated into the Go binary using Go embedding. By default, `make build` includes the UI by passing the `builtui` tag to the Go compiler. If you wish to build the backend without the UI, you can run:
```bash
rtk go build -o panmail ./cmd/api
```
The application will detect that the UI is not embedded and log a message accordingly.

## 🧪 Testing

Run backend tests:
```bash
rtk go test -v ./...
```

## 📜 License

MIT
