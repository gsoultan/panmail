-- Membership: a user may belong to more than one tenant.
--
-- Before this, `users.tenant_id` was the whole story — one user, one tenant.
-- Putting the same person in a second tenant meant creating a second account,
-- which `users.email UNIQUE` refuses outright, so the only way through was a
-- plus-addressed alias and a second password. This table replaces that.
--
-- `users.tenant_id` is deliberately kept and keeps its meaning: it is the
-- user's HOME tenant, the one they land in at sign-in, and `users.role` is
-- their role there. Sign-in is therefore untouched by this migration and
-- cannot regress. Membership rows govern the ADDITIONAL tenants a user may
-- switch into, and carry the role they hold in each one — an administrator at
-- home is not automatically an administrator as a guest elsewhere.
--
-- The backfill gives every existing user a row for their home tenant, so that
-- "which tenants can this user reach" is answerable from this table alone
-- without a UNION against users.

CREATE TABLE IF NOT EXISTS user_tenants (
    user_id {{UUID}} NOT NULL,
    tenant_id {{UUID}} NOT NULL,

    -- The role held in THIS tenant. Never USER_ROLE_SUPER_ADMIN: super admin
    -- is a global capability (it reaches ListTenants, which is not scoped to
    -- any tenant), so it lives on users.role and is refused here. Allowing it
    -- per-membership would turn "assign this user to a tenant" into a way to
    -- mint a global administrator.
    role VARCHAR(50) NOT NULL DEFAULT 'USER_ROLE_VIEWER',

    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL,

    PRIMARY KEY (user_id, tenant_id),
    FOREIGN KEY (user_id) REFERENCES users(id),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

-- Listing the members of one tenant is the read this table exists to serve.
CREATE INDEX IF NOT EXISTS idx_user_tenants_tenant_id ON user_tenants(tenant_id);

-- Every existing user becomes a member of the tenant they already had. Users
-- whose tenant_id is NULL — the column was added by 0001 without a backfill —
-- get no row, which is correct: they have no tenant to be a member of.
INSERT INTO user_tenants (user_id, tenant_id, role, created_at, updated_at)
SELECT id, tenant_id, role, created_at, updated_at
FROM users
WHERE tenant_id IS NOT NULL
ON CONFLICT DO NOTHING;
