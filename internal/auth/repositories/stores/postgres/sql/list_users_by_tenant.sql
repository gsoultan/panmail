SELECT id, tenant_id, email, password, name, role, two_factor_enabled, two_factor_secret, created_at, updated_at
FROM users
WHERE tenant_id = $1
ORDER BY created_at DESC;
