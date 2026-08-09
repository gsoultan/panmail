-- Baseline schema.
--
-- The CREATE statements are idempotent, and the ALTER statements below them
-- carry installations created before those columns existed. On a fresh
-- database the ALTERs are duplicates and are tolerated; on an older one they
-- do real work. This is the squashed history as of the introduction of
-- versioned migrations.

CREATE TABLE IF NOT EXISTS tenants (
    id {{UUID}} PRIMARY KEY,
    name TEXT NOT NULL,
    retry_pattern {{JSON}},
    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    email VARCHAR(255) NOT NULL UNIQUE,
    password TEXT NOT NULL,
    name TEXT NOT NULL,
    role VARCHAR(50) NOT NULL DEFAULT 'USER_ROLE_VIEWER',
    two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    two_factor_secret TEXT,
    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE TABLE IF NOT EXISTS email_providers (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL,
    config {{JSON}} NOT NULL,
    allowed_domains {{JSON}},
    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id),
    UNIQUE(tenant_id, name)
);

CREATE TABLE IF NOT EXISTS api_keys (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    prefix VARCHAR(10) NOT NULL,
    last_used_at {{TIMESTAMP}},
    expires_at {{TIMESTAMP}},
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE TABLE IF NOT EXISTS templates (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    name TEXT NOT NULL,
    subject TEXT NOT NULL,
    body_html TEXT NOT NULL,
    body_text TEXT NOT NULL,
    design TEXT,
    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE TABLE IF NOT EXISTS suppressions (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    email VARCHAR(255) NOT NULL,
    reason TEXT,
    created_at {{TIMESTAMP}} NOT NULL,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id),
    UNIQUE(tenant_id, email)
);

CREATE TABLE IF NOT EXISTS webhooks (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    events {{JSON}} NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE TABLE IF NOT EXISTS outbox (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    request {{JSON}} NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    retry_count INTEGER NOT NULL DEFAULT 0,
    next_retry_at {{TIMESTAMP}} NOT NULL,
    last_error TEXT,
    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

ALTER TABLE tenants ADD COLUMN retry_pattern {{JSON}};
ALTER TABLE users ADD COLUMN tenant_id {{UUID}};
ALTER TABLE users ADD COLUMN role VARCHAR(50) NOT NULL DEFAULT 'USER_ROLE_VIEWER';
ALTER TABLE users ADD COLUMN two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN two_factor_secret TEXT;
ALTER TABLE email_providers ADD COLUMN tenant_id {{UUID}};
ALTER TABLE email_providers ADD COLUMN allowed_domains {{JSON}};
ALTER TABLE api_keys ADD COLUMN expires_at {{TIMESTAMP}};
ALTER TABLE api_keys ADD COLUMN is_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE templates ADD COLUMN design TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_email_providers_tenant_name ON email_providers(tenant_id, name);
CREATE INDEX IF NOT EXISTS idx_users_tenant_id ON users(tenant_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_tenant_id ON api_keys(tenant_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_templates_tenant_id ON templates(tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_templates_tenant_name ON templates(tenant_id, name);
CREATE INDEX IF NOT EXISTS idx_webhooks_tenant_id ON webhooks(tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhooks_tenant_name ON webhooks(tenant_id, name);
CREATE INDEX IF NOT EXISTS idx_outbox_tenant_id ON outbox(tenant_id);
CREATE INDEX IF NOT EXISTS idx_outbox_status_next_retry ON outbox(status, next_retry_at);
CREATE INDEX IF NOT EXISTS idx_suppressions_tenant_id ON suppressions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenants_name ON tenants(name);
