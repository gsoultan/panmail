INSERT INTO outbox (id, tenant_id, request, status, retry_count, next_retry_at, last_error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);
