-- Delivery events, moving off each instance's Pebble store.
--
-- events.db is local to a gateway, so with three replicas the dashboard shows
-- whatever the instance serving the request happened to see. See
-- docs/design/0002-shared-event-store.md for the measurement behind this:
-- a batched writer sustains 31,248 events/s at the batch size the Pebble writer
-- already uses, against ~7,300/s that the fastest measured send path produces.
--
-- Nothing reads this yet. It is written in parallel with Pebble so the two can
-- be compared under the same traffic before any read moves.

CREATE TABLE IF NOT EXISTS email_events (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,

    -- Nullable: an event recorded before a provider is chosen has none, which
    -- is what a DROPPED event on a suppressed recipient looks like.
    provider_id {{UUID}},
    provider_name TEXT,

    message_id {{UUID}} NOT NULL,

    -- The generated enum's name, not its number. A row written by a newer
    -- build naming a type this one does not know reads as an unknown string
    -- rather than silently matching some other type by number.
    type TEXT NOT NULL,

    -- 320 is the RFC 5321 maximum: 64 local part, 1 at, 255 domain.
    recipient VARCHAR(320) NOT NULL,
    subject TEXT,

    timestamp {{TIMESTAMP}} NOT NULL,

    -- Opaque to this table. TEXT rather than {{JSON}} for the reason migration
    -- 0007 moved the other opaque columns: SQLite validates neither, and a
    -- JSONB column on PostgreSQL reformats what it stores.
    metadata TEXT,
    error_message TEXT
);

-- The two the dashboard actually issues. Measured on 400,000 rows: 0.044 ms
-- for a tenant's recent events and 0.054 ms for one message's timeline.
--
-- tenant_id leads and timestamp descends because every listing is scoped to a
-- tenant and ordered newest first; an index that answers the sort lets the scan
-- stop at the page rather than sorting the tenant's whole history. That is the
-- same lesson migration 0011 recorded about the outbox claim.
CREATE INDEX IF NOT EXISTS idx_email_events_tenant_ts
    ON email_events (tenant_id, timestamp DESC);

CREATE INDEX IF NOT EXISTS idx_email_events_message
    ON email_events (message_id);
