SELECT id, name, retry_pattern, created_at, updated_at
FROM tenants
WHERE id = $1;
