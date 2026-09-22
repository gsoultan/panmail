UPDATE email_providers
SET name = $3, config = $4, allowed_domains = $5, webhook_secret = $6,
    send_rate_per_minute = $7, send_burst = $8, updated_at = $9
WHERE tenant_id = $1 AND id = $2;
