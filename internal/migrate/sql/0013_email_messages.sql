-- Stored message bodies, the other half of what events.db holds.
--
-- Step three of docs/design/0002-shared-event-store.md moves reads to the
-- shared store, and the delivery-details Content tab reads this. Leaving it in
-- Pebble would move the event timeline off the instance and leave the body on
-- it, which is half a fix.
--
-- One row per message, not per recipient: the body is what was sent, and the
-- per-recipient facts are events.

CREATE TABLE IF NOT EXISTS email_messages (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    provider_id {{UUID}},

    from_address VARCHAR(320) NOT NULL,

    -- Recipient lists as JSON arrays, stored whole. They are only ever read
    -- and written with the message, never queried across messages, so rows per
    -- recipient would buy joins nobody performs.
    --
    -- TEXT rather than {{JSON}} for the reason migration 0007 gave: SQLite
    -- validates neither, and JSONB reformats what it stores.
    to_addresses TEXT,
    cc_addresses TEXT,
    bcc_addresses TEXT,

    subject TEXT,
    body_html TEXT,
    body_text TEXT,

    -- Attachment content included, because that is what the store held and
    -- what the download button serves. This is the largest column in the
    -- schema by some distance; app.message_retention_days is what bounds it,
    -- and it defaults to keeping forever.
    attachments TEXT,

    created_at {{TIMESTAMP}} NOT NULL
);

-- Both reads the dashboard issues: one message by id, and the latest message
-- for a recipient. The second is a fallback lookup used when an event arrives
-- carrying no message id.
CREATE INDEX IF NOT EXISTS idx_email_messages_tenant_created
    ON email_messages (tenant_id, created_at DESC);
