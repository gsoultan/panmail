INSERT INTO webhook_deliveries
    (id, tenant_id, webhook_id, event, payload, status, attempt_count, next_attempt_at, last_error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);
