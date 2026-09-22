SELECT id, tenant_id, name, type, config, allowed_domains, webhook_secret,
       send_rate_per_minute, send_burst, created_at, updated_at
FROM email_providers
WHERE tenant_id = $1 AND id = $2;
