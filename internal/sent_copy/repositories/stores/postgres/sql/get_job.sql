SELECT id, tenant_id, message_id, request, attempts, claim_token, last_error, created_at
FROM sent_copy_jobs
WHERE id = $1
