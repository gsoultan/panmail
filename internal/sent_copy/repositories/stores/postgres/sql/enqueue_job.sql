INSERT INTO sent_copy_jobs (id, tenant_id, message_id, request, attempts, next_attempt_at, created_at)
VALUES ($1, $2, $3, $4, 0, $5, $5)
