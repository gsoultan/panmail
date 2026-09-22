INSERT INTO email_providers (id, tenant_id, name, type, config, allowed_domains, webhook_secret,
                             send_rate_per_minute, send_burst, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);
