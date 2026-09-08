-- The SMTP submission listener, moved from process flags into the database so
-- an administrator can open the door without shell access on the host.
--
-- One row, like system_settings, and for the same reason: there is one
-- deployment-wide answer. It is a separate table rather than more columns on
-- system_settings because of what tls_private_key holds. GetSettings is
-- readable by any authenticated caller, and putting key material on the same
-- row as values served that widely means the next convenience join is a
-- disclosure. Separate tables make that mistake require a deliberate act.
--
-- Flags still win. A process started with --smtp-addr ignores this row
-- entirely, so an existing deployment upgrades without changing behaviour and
-- without this table needing to be seeded to match.

CREATE TABLE IF NOT EXISTS smtp_submission (
    id INTEGER PRIMARY KEY CHECK (id = 1),

    enabled BOOLEAN NOT NULL DEFAULT FALSE,

    -- 'loopback' or 'all_interfaces'. Stored as the scope rather than as a
    -- host:port string: the address decides how far the port is reachable, and
    -- a free-text host is unvalidatable input for a choice with two answers.
    -- internal/smtp_submission/entities turns it back into an address.
    bind_scope TEXT NOT NULL DEFAULT 'loopback',

    port INTEGER NOT NULL DEFAULT 587,

    -- PEM. The certificate is public by construction and stored as it arrived.
    tls_certificate TEXT NOT NULL DEFAULT '',

    -- PEM, encrypted at rest with the gateway's data key, exactly like
    -- email_providers.config and webhooks' signing secrets. The write path
    -- refuses when no key is configured rather than falling back to plaintext,
    -- so a value here is either ciphertext or empty.
    tls_private_key TEXT NOT NULL DEFAULT '',

    -- Whether AUTH is accepted without TLS. Only ever true alongside
    -- bind_scope = 'loopback'; the domain refuses the combination that would
    -- put a tenant's API key on a network in the clear.
    allow_insecure_auth BOOLEAN NOT NULL DEFAULT FALSE,

    updated_at {{TIMESTAMP}} NOT NULL
);
