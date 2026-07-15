SELECT id, tenant_id, name, key_hash, prefix, last_used_at, expires_at, is_enabled, created_at, updated_at
FROM api_keys
WHERE id = $1 AND tenant_id = $2;
