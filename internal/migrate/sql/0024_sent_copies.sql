-- Sent copies: putting a message sent through panmail into the sender's own
-- Sent folder.
--
-- A mail client that sends through panmail's SMTP listener files its own copy
-- of the message over IMAP, but not every client does, and a client that
-- relied on its old mail server to file the copy finds nothing there any more.
-- A tenant administrator can have panmail file the copy instead, by logging in
-- to the sender's mailbox with one administrator account or with that
-- mailbox's own login, or by mailing the copy back to the sender.
--
-- The tables cascade with the tenant: the setting, the logins and any copy
-- still waiting mean nothing once the tenant is gone, and counting them as
-- dependents would make a tenant undeletable for the sake of its own settings.

-- One row per tenant that has chosen a mode. No row means NONE.
CREATE TABLE IF NOT EXISTS sent_copy_settings (
    tenant_id {{UUID}} PRIMARY KEY,

    -- 'none', 'shared_login', 'per_user_login' or 'bcc_sender'.
    mode TEXT NOT NULL DEFAULT 'none',

    imap_host TEXT NOT NULL DEFAULT '',
    imap_port INTEGER NOT NULL DEFAULT 0,
    imap_use_ssl BOOLEAN NOT NULL DEFAULT TRUE,
    sent_folder TEXT NOT NULL DEFAULT '',

    master_username TEXT NOT NULL DEFAULT '',
    -- Encrypted at rest with the gateway's data key, like email_providers.config.
    -- The write path refuses when no key is configured, so a value here is
    -- either ciphertext or empty.
    master_password TEXT NOT NULL DEFAULT '',
    master_separator TEXT NOT NULL DEFAULT '',

    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL,

    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
);

-- Mailbox logins for per_user_login, keyed by the lower-cased sender address.
CREATE TABLE IF NOT EXISTS sent_copy_mailboxes (
    tenant_id {{UUID}} NOT NULL,
    address TEXT NOT NULL,
    username TEXT NOT NULL DEFAULT '',
    -- Encrypted at rest, as above.
    password TEXT NOT NULL DEFAULT '',
    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL,

    PRIMARY KEY (tenant_id, address),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
);

-- Copies waiting to be filed. A row is written when a message that asked for
-- a copy is accepted, and deleted once the copy is filed or has failed for
-- good. The request is kept here rather than read from the outbox because the
-- outbox row is deleted as soon as the message is delivered.
--
-- Times are Unix seconds so the lease comparison means the same thing on every
-- engine. A row with a claim older than the lease belongs to a worker that
-- died, and may be taken over.
CREATE TABLE IF NOT EXISTS sent_copy_jobs (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    message_id {{UUID}} NOT NULL,
    request {{JSON}} NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at BIGINT NOT NULL,
    claim_token TEXT NOT NULL DEFAULT '',
    claimed_at BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,

    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sent_copy_jobs_next_attempt_at
    ON sent_copy_jobs (next_attempt_at);
