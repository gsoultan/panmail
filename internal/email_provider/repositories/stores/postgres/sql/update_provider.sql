UPDATE email_providers
SET name = $3, config = $4, allowed_domains = $5, webhook_secret = $6, updated_at = $7
WHERE tenant_id = $1 AND id = $2;
