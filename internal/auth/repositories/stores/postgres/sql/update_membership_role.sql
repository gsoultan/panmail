UPDATE user_tenants
SET role = $1, updated_at = $2
WHERE user_id = $3 AND tenant_id = $4;
