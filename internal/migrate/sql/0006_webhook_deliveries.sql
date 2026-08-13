-- Webhook deliveries, so a notification survives a failure and a restart.
--
-- Delivery was an in-memory channel of a thousand jobs. A tenant endpoint that
-- returned an error, or was briefly unreachable, had its notification logged
-- and dropped; a full channel dropped with a warning; and a restart lost
-- everything queued. For the mechanism whose whole job is telling a tenant what
-- happened to their mail, that is silent, unrecoverable loss.
--
-- The shape mirrors the outbox, which already solves this: lease-based claiming
-- so two workers cannot deliver the same notification twice, a retry schedule,
-- and a terminal state that says why it stopped.
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    -- Which subscription this is owed to. Stored as a reference rather than a
    -- copied URL, so correcting a wrong endpoint redirects the retries that
    -- are still pending, and deleting the subscription stops them.
    webhook_id {{UUID}} NOT NULL,
    -- The event and its JSON payload, stored rather than referenced: the
    -- notification describes a moment, and re-deriving it later from mutable
    -- records would deliver something subtly different from what was promised.
    event TEXT NOT NULL,
    payload {{JSON}} NOT NULL,

    status TEXT NOT NULL DEFAULT 'PENDING',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at {{TIMESTAMP}} NOT NULL,
    last_error TEXT,

    -- A claim that expires without being resolved is reclaimable, so a worker
    -- that dies mid-delivery does not strand the notification.
    claim_token {{UUID}},
    claimed_until {{TIMESTAMP}},

    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_due
    ON webhook_deliveries(next_attempt_at, status);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_tenant
    ON webhook_deliveries(tenant_id);

-- Signing secret, so a tenant can tell a real notification from a forged one.
--
-- Panmail verifies the webhooks it receives from SendGrid and Mailgun but
-- signed none of the ones it sends, which left the same threat open in the
-- other direction: anyone who learned a tenant's endpoint URL could post
-- "your mail bounced" to it and be believed. Encrypted at rest like every
-- other credential.
ALTER TABLE webhooks ADD COLUMN secret TEXT NOT NULL DEFAULT '';
