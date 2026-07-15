DELETE FROM api_keys
WHERE id = $1 AND tenant_id = $2;
