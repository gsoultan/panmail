UPDATE tenants
SET name = $2, retry_pattern = $3, send_rate_per_minute = $4, send_burst = $5, updated_at = $6
WHERE id = $1;
