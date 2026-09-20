UPDATE api_keys
SET name = $1, scopes = $2, updated_at = $3
WHERE id = $4 AND tenant_id = $5;
