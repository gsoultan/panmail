UPDATE webhooks
SET name = $1, url = $2, events = $3, active = $4, updated_at = $5
WHERE tenant_id = $6 AND id = $7;
