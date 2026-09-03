-- The global settings an administrator edits, moved out of config.yaml.
--
-- They were in the file and written back by the settings page. That is a
-- single-writer design in a system that runs several gateways against one
-- database: a save reached one instance and the others kept the old values,
-- with nothing to signal the divergence. Under Kubernetes it reached none of
-- them, because the config is mounted from a Secret and those are always
-- read-only, so Settings -> Save returned a permission error.
--
-- One row, because there is one deployment-wide answer to each of these. The
-- id column exists only to make that constraint expressible and is always 1;
-- the CHECK is what stops a second row rather than convention.
--
-- Per-tenant configuration is not here. A tenant's send rate and a provider's
-- allowed domains live on their own rows and always did.

CREATE TABLE IF NOT EXISTS system_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),

    base_url TEXT NOT NULL DEFAULT '',

    -- The backoff schedule as a JSON array of duration strings. Stored whole
    -- because it is only ever read and written as one list, never queried
    -- across its elements.
    retry_pattern {{JSON}},

    -- Nullable on purpose, and this is the one thing in this table not to
    -- "tidy up" with a NOT NULL DEFAULT 0. These two are the retentions whose
    -- default is not zero -- 14 days of delivery events, 7 of webhook
    -- notifications. NULL means nobody has set one, so the default applies; 0
    -- means an administrator chose to keep forever. Collapsing them makes a
    -- deliberate "keep forever" read as the default on the next restart, and
    -- the settings page would go on showing the value that is no longer in
    -- force. See internal/system_settings/entities.
    log_retention_days INTEGER,
    webhook_retention_days INTEGER,

    -- The other five default to zero and zero means forever, so absent and
    -- explicit are the same statement and a plain column says it.
    message_retention_days INTEGER NOT NULL DEFAULT 0,
    outbox_retention_days INTEGER NOT NULL DEFAULT 0,
    app_log_retention_days INTEGER NOT NULL DEFAULT 0,
    inbound_retention_days INTEGER NOT NULL DEFAULT 0,
    archive_retention_days INTEGER NOT NULL DEFAULT 0,
    quarantine_retention_days INTEGER NOT NULL DEFAULT 0,

    updated_at {{TIMESTAMP}} NOT NULL
);
