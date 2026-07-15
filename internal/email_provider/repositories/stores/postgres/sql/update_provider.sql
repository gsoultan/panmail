UPDATE email_providers SET name = $3, config = $4, allowed_domains = $5, updated_at = $6 WHERE tenant_id = $1 AND id = $2;
