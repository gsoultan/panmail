INSERT INTO user_tenants (user_id, tenant_id, role, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, tenant_id) DO UPDATE SET role = $3, updated_at = $5;
