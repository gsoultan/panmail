INSERT INTO tenants (id, name, retry_pattern, send_rate_per_minute, send_burst, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);
