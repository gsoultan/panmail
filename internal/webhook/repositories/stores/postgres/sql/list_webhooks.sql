SELECT id, tenant_id, name, url, events, active, created_at, updated_at
FROM webhooks
WHERE tenant_id = $1
ORDER BY created_at DESC;
