SELECT id, tenant_id, request, status, retry_count, next_retry_at, last_error, created_at, updated_at
FROM outbox
WHERE (status = 'PENDING' OR status = 'DEFERRED') AND next_retry_at <= $1
ORDER BY created_at ASC
LIMIT $2;
