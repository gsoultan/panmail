SELECT id, tenant_id, request, status, retry_count, next_retry_at, last_error, created_at, updated_at
FROM outbox
WHERE claim_token = $1
ORDER BY created_at ASC;
