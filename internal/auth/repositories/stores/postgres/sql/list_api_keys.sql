SELECT id, tenant_id, name, key_hash, prefix, last_used_at, expires_at, is_enabled, created_at, updated_at
FROM api_keys
WHERE tenant_id = $1
ORDER BY created_at DESC;
