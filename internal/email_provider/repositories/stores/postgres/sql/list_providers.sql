SELECT id, tenant_id, name, type, config, allowed_domains, webhook_secret, created_at, updated_at
FROM email_providers
WHERE tenant_id = $1
ORDER BY created_at DESC;
