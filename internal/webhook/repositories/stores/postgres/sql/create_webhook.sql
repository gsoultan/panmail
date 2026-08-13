INSERT INTO webhooks (id, tenant_id, name, url, events, active, secret, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);
