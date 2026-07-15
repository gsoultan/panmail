SELECT id, tenant_id, name, key_hash, prefix, last_used_at, expires_at, is_enabled, created_at, updated_at
FROM api_keys
WHERE key_hash = $1;
