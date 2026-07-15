UPDATE tenants
SET name = $2, retry_pattern = $3, updated_at = $4
WHERE id = $1;
