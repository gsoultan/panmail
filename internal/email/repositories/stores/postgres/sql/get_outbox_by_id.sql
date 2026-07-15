SELECT id, tenant_id, request, status, retry_count, next_retry_at, last_error, created_at, updated_at
FROM outbox
WHERE id = $1;
