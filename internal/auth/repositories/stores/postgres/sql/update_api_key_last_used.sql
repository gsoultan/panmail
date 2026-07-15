UPDATE api_keys
SET last_used_at = $2, updated_at = $2
WHERE id = $1;
