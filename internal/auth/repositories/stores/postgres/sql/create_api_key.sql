INSERT INTO api_keys (id, tenant_id, name, key_hash, prefix, expires_at, is_enabled, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);
