# 0001 — Multi-channel notification model

**Status:** Draft, for review. No implementation yet.
**Scope:** The contract for turning Panmail from an email gateway into a
notification gateway carrying email, Telegram and FCM push.

---

## 1. Principle

Generalize the spine; keep channel logic at the edges.

The delivery spine — multi-tenancy, the outbox with lease claiming, retry
patterns, the event store, outbound webhooks, template storage — is already
channel-agnostic. Five things are email-shaped and need a channel dimension:

1. Addressing (`From/To/Cc/Bcc/Subject`)
2. Suppression (keyed by email address, one meaning)
3. Failure classification (SMTP status codes)
4. Tracking (HTML pixels and link rewriting)
5. The `ProviderType` enum and domain validation

Everything else stays as it is.

**Non-goal:** Panmail does not become a user database. It does not own a
registry mapping users to device tokens or chat ids. The caller supplies the
address on every send, exactly as it supplies an email address today. This
keeps the product a gateway rather than a notification platform, and it is the
single largest scope decision in this document. See §9 Q3.

---

## 2. Two-level provider model

`Channel` is the medium. `ProviderType` is the vendor within that medium. One
channel has many implementations, which is already true for email and will be
true for push.

```proto
// api/panmail/v1/channel.proto
enum Channel {
  CHANNEL_UNSPECIFIED = 0;
  CHANNEL_EMAIL = 1;
  CHANNEL_TELEGRAM = 2;
  CHANNEL_PUSH = 3;
  // Reserve room rather than renumber later.
  reserved 4 to 9;  // SMS, SLACK, WHATSAPP, WEBHOOK, DISCORD, TEAMS
}
```

`ProviderType` extends the existing enum. Tags 2–5 are reserved from the
earlier removal of the ESP providers and **must stay reserved** — new vendors
take fresh numbers.

```proto
enum ProviderType {
  PROVIDER_TYPE_UNSPECIFIED = 0;
  PROVIDER_TYPE_SMTP = 1;
  reserved 2 to 5;  // former SES/SENDGRID/MAILGUN/POSTMARK — never reuse
  reserved "PROVIDER_TYPE_SES", "PROVIDER_TYPE_SENDGRID",
           "PROVIDER_TYPE_MAILGUN", "PROVIDER_TYPE_POSTMARK";
  PROVIDER_TYPE_IMAP = 6;
  PROVIDER_TYPE_POP3 = 7;

  // Email, HTTP API. gsmail already ships working Senders for all four.
  PROVIDER_TYPE_EMAIL_SENDGRID = 8;
  PROVIDER_TYPE_EMAIL_MAILGUN  = 9;
  PROVIDER_TYPE_EMAIL_POSTMARK = 10;
  PROVIDER_TYPE_EMAIL_SES      = 11;

  PROVIDER_TYPE_TELEGRAM_BOT = 20;
  PROVIDER_TYPE_PUSH_FCM     = 30;
}
```

### Channel is derived, not supplied

`Channel` is a pure function of `ProviderType`. A client never sets it; the
server computes it on write and stores it as an indexed column so
`ListProviders(channel: TELEGRAM)` is a real query rather than a scan.

```go
// internal/provider/channel.go — the single source of truth.
func ChannelOf(t panmailv1.ProviderType) panmailv1.Channel
```

Storing a value the client could contradict would create an invariant someone
has to remember to enforce. Deriving it means there is nothing to enforce.

### Provider message

```proto
message Provider {
  string id = 1;
  string tenant_id = 2;
  string name = 3;
  ProviderType type = 4;
  Channel channel = 5;  // output only, derived from type

  oneof config {
    SmtpConfig     smtp     = 10;
    SendGridConfig sendgrid = 11;
    MailgunConfig  mailgun  = 12;
    PostmarkConfig postmark = 13;
    SesConfig      ses      = 14;
    ImapConfig     imap     = 15;
    Pop3Config     pop3     = 16;
    TelegramConfig telegram = 20;
    FcmConfig      fcm      = 30;
  }

  // Email only: domains this provider may send for.
  repeated string allowed_domains = 40;

  // Write-only, never returned. Already encrypted at rest.
  string webhook_secret = 41;

  google.protobuf.Timestamp create_time = 50;
  google.protobuf.Timestamp update_time = 51;
}
```

The encrypted `config` blob added for provider credentials already handles
arbitrary shapes, including FCM's service-account JSON. No new secret
machinery is needed.

```proto
message TelegramConfig {
  string bot_token = 1;  // write-only
  // Telegram enforces ~30 msg/s globally and 20/min per group.
  int32 rate_limit_per_second = 2;
}

message FcmConfig {
  string service_account_json = 1;  // write-only
  string project_id = 2;
}
```

---

## 3. Send contract: generic envelope, channel-specific payload

The temptation is a single flattened recipient model. It does not survive
contact with reality — CC/BCC is email transport structure, topics and
conditions are FCM addressing, and a Telegram chat id is neither. Flattening
loses information in every direction.

Instead: **the envelope is generic, the payload is native to its channel.**

```proto
message SendRequest {
  // Required. Deduplicates retries across every channel in this request.
  string idempotency_key = 1;

  string template_id = 2;
  google.protobuf.Struct data = 3;

  // Either name a routing rule, or spell the deliveries out inline.
  string routing_rule_id = 4;
  repeated Delivery deliveries = 5;
  DeliveryStrategy strategy = 6;
}

enum DeliveryStrategy {
  DELIVERY_STRATEGY_UNSPECIFIED = 0;
  // Send on every listed channel.
  DELIVERY_STRATEGY_FANOUT = 1;
  // Try in order; stop at the first acceptance.
  DELIVERY_STRATEGY_FALLBACK = 2;
}

message Delivery {
  // Optional. When empty, routing picks a provider for the channel.
  string provider_id = 1;

  oneof payload {
    EmailPayload    email    = 10;
    TelegramPayload telegram = 11;
    PushPayload     push     = 12;
  }
}
```

The channel of a `Delivery` is implied by which payload is set — again,
nothing to keep in sync.

```proto
message EmailPayload {
  string from = 1;
  repeated string to = 2;
  repeated string cc = 3;
  repeated string bcc = 4;
  string subject = 5;
  string body_html = 6;
  string body_text = 7;
  repeated Attachment attachments = 8;
}

message TelegramPayload {
  // Numeric chat id or @channelusername.
  string chat_id = 1;
  string text = 2;
  TelegramParseMode parse_mode = 3;  // PLAIN | MARKDOWN_V2 | HTML
  bool disable_notification = 4;
  repeated TelegramButton buttons = 5;  // URL buttons are click-trackable
}

message PushPayload {
  oneof target {
    string token = 1;
    string topic = 2;
    string condition = 3;
  }
  string title = 4;
  string body = 5;
  // FCM caps the whole message at 4KB. The server rejects oversized
  // payloads rather than letting FCM fail opaquely.
  map<string, string> data = 6;
}
```

`FANOUT` + `FALLBACK` as an explicit strategy removes the ambiguity in "a list
of deliveries" — without it, nobody can tell whether two entries mean *both*
or *either*.

---

## 4. Suppression vs. address invalidity

**This is the most important distinction in the document**, and conflating the
two is the same mistake that previously let one wrong SMTP password suppress
every recipient a tenant had.

They are different facts with different lifetimes:

- **Suppression is a decision about consent.** Durable, auditable, often
  applies across every channel. "This person does not want to hear from us."
- **Invalidity is a fact about an address.** Transient, garbage-collected,
  says nothing about consent. "This token no longer exists."

| Signal | Channel | Action | Scope |
|---|---|---|---|
| `5.1.1` user unknown | email | suppress | channel only |
| Spam complaint | email | suppress | **all channels** |
| Unsubscribe | any | suppress | **all channels** |
| Telegram `403 bot was blocked by the user` | telegram | suppress | channel only |
| Telegram `400 chat not found` | telegram | **invalid address**, not suppression | — |
| FCM `UNREGISTERED` / `INVALID_ARGUMENT` | push | **invalid address**, not suppression | — |
| SMTP mailbox full | email | neither — retry | — |

A dead FCM token must never suppress anything. The app was reinstalled; the
person did not withdraw consent. Since Panmail does not own a token registry
(§1), the correct response is to report the address as invalid on the event and
let the caller stop sending it — Panmail has nothing of its own to delete.

```proto
message Suppression {
  string id = 1;
  string tenant_id = 2;
  Channel channel = 3;
  string address = 4;
  SuppressionScope scope = 5;
  string reason = 6;
  google.protobuf.Timestamp create_time = 7;
}

enum SuppressionScope {
  SUPPRESSION_SCOPE_UNSPECIFIED = 0;
  // Blocks this address on its own channel only.
  SUPPRESSION_SCOPE_CHANNEL = 1;
  // Blocks every address belonging to this person, on every channel.
  // Requires a correlation key — see §9 Q2.
  SUPPRESSION_SCOPE_GLOBAL = 2;
}
```

---

## 5. Events: "delivered" means three different things

Today `DELIVERED` is recorded when SMTP hands over, which is acceptance, not
delivery. Adding channels makes that inaccuracy structural, because each
channel confirms a different thing:

| Channel | Provider accepted | Actually delivered |
|---|---|---|
| Email | SMTP `250` | DSN or provider webhook |
| Telegram | `ok: true` + `message_id` | same response — Telegram has it |
| FCM | HTTP `200` + message name | **not available** without BigQuery export |

So split the event:

- `ACCEPTED` — the provider took responsibility. Always available.
- `DELIVERED` — confirmed reaching the recipient. Email via webhook/DSN,
  Telegram immediately, FCM **never** under a default setup.

Analytics must render "Delivered" per channel with the caveat, or it will
overstate FCM. This change also corrects the existing email inaccuracy, so it
pays for itself before any new channel ships.

Every event gains `channel` and `provider_type`.

---

## 6. Templates: one logical message, N renditions

Users think in terms of "the welcome notification", not "the welcome email"
and separately "the welcome push".

```proto
message Template {
  string id = 1;
  string tenant_id = 2;
  string name = 3;
  // Keyed by Channel enum name.
  map<string, ChannelTemplate> renditions = 4;
}

message ChannelTemplate {
  oneof body {
    EmailTemplate    email    = 1;
    TelegramTemplate telegram = 2;
    PushTemplate     push     = 3;
  }
}
```

Rendering a `SendRequest` picks the rendition matching the delivery's channel.
A missing rendition is an error at send time, not a silent skip.

**Blocked on an existing bug.** The renderer tries Handlebars first and only
consults `isHTML` in the Go-template fallback, so plaintext output is
HTML-escaped (`Tom & Jerry` → `Tom &amp; Jerry`). That is cosmetic in email
subjects and **breaking** for Telegram MarkdownV2, whose escaping rules are
strict and unforgiving. Fix before Telegram templates ship.

---

## 7. Routing and fallback

The reason to build one gateway rather than three integrations.

```proto
message RoutingRule {
  string id = 1;
  string tenant_id = 2;
  string name = 3;
  int32 priority = 4;         // lowest number wins
  RoutingMatch match = 5;     // template id, or a data-field predicate
  DeliveryStrategy strategy = 6;
  repeated RoutingStep steps = 7;
}

message RoutingStep {
  Channel channel = 1;
  string provider_id = 2;  // optional; any healthy provider if empty
}
```

Evaluation order: explicit `deliveries` in the request beat
`routing_rule_id`, which beats the tenant default rule. A step whose address is
suppressed or missing is skipped, not failed — that is what makes a fallback
chain useful.

The provider-failover loop already in `doSend` is a primitive version of this
and should be replaced by it rather than living alongside it.

---

## 8. Compatibility and migration

Nothing existing breaks.

| Surface | Plan |
|---|---|
| `EmailService.SendEmail` | Kept. Becomes a thin adapter building a one-delivery `SendRequest`. |
| `EmailProviderService` | Kept. Operates on `channel = EMAIL` providers only. |
| `NotificationService.Send` | New, generic. |
| `ProviderService` | New, all channels. |

Schema:

```sql
-- providers
ALTER TABLE email_providers RENAME TO providers;   -- works on PG and SQLite
ALTER TABLE providers ADD COLUMN channel VARCHAR(32) NOT NULL DEFAULT 'CHANNEL_EMAIL';
CREATE INDEX idx_providers_tenant_channel ON providers(tenant_id, channel);

-- suppressions: existing rows are email, channel-scoped
ALTER TABLE suppressions ADD COLUMN channel VARCHAR(32) NOT NULL DEFAULT 'CHANNEL_EMAIL';
ALTER TABLE suppressions ADD COLUMN scope   VARCHAR(32) NOT NULL DEFAULT 'SUPPRESSION_SCOPE_CHANNEL';
```

**One real wrinkle.** `suppressions` currently has `UNIQUE(tenant_id, email)`
and needs `UNIQUE(tenant_id, channel, address)`. SQLite cannot alter a
constraint, so this requires the create-copy-drop-rename dance in a migration
step. It is the only non-additive change in the set, and the reason to write it
carefully rather than discover it at deploy time.

Every step goes through the versioned migration runner, so a failure is
reported rather than swallowed.

New API key scopes: `notifications:send`, `providers:*` generalized from the
email-only names, `routing:read`, `routing:write`. The deny-by-default policy
table means each new RPC is unreachable until it is listed — no new endpoint
can be silently exposed.

---

## 9. Open questions — need a decision before implementation

**Q1. Does `SendRequest.idempotency_key` become required?**
Recommended yes. Multi-channel makes duplicate sends worse, and the current
dedup depends on a `DELIVERED` event having been written — which fails exactly
when it matters, after a crash mid-send. Making it required is a breaking
change for `SendEmail` callers unless the adapter synthesizes one.

**Q2. What correlates a person across channels?**
`SUPPRESSION_SCOPE_GLOBAL` and "notify this user however you can" both need a
stable subject id — an email address is not one once push exists. Options: a
caller-supplied `recipient_id` on the envelope (simple, no storage), or a
Panmail-owned contact record (much larger scope, contradicts §1).
Recommended: caller-supplied `recipient_id`, optional, and global suppression
only works when it is present.

**Q3. Confirm the no-token-registry boundary.**
If Panmail never stores device tokens, callers must supply them per send and
handle invalidation themselves. That keeps this a gateway. Owning a registry
makes it a platform, roughly doubles the scope, and pulls in contact
management, preferences and consent tracking. Recommended: stay a gateway in
v1, revisit once routing is proven.

**Q4. Does `Provider` replace `EmailProvider` in the UI immediately?**
The provider form is channel-specific (SMTP host/port vs. bot token vs.
service-account JSON upload). Recommended: one provider list filtered by
channel, with a channel-specific form per type.

---

## 10. Suggested build order

Each step is shippable and useful on its own.

1. **ESP providers via gsmail** (`SENDGRID`/`MAILGUN`/`POSTMARK`/`SES`) — proves
   the factory handles HTTP-API transports while the payload is still email.
   Requires removing the From/host domain check, which is almost certainly why
   these were pulled the first time.
2. **Per-provider rate limiting** — mandatory before Telegram, valuable for
   email now.
3. **Channel dimension + generic `NotificationService`**, email routed through
   it. No new channel yet.
4. **`ACCEPTED` vs `DELIVERED`** split.
5. **Telegram** — simplest to verify end to end; a bot token and an HTTP call.
6. **FCM.**
7. **Routing rules and fallback chains.**
