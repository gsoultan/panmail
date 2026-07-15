UPDATE api_keys
SET is_enabled = $1, updated_at = $2
WHERE id = $3 AND tenant_id = $4;
