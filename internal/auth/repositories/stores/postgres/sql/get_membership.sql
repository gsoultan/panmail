SELECT user_id, tenant_id, role, created_at, updated_at
FROM user_tenants
WHERE user_id = $1 AND tenant_id = $2;
