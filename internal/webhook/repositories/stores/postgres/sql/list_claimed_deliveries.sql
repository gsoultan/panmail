SELECT id, tenant_id, webhook_id, event, payload, status, attempt_count, next_attempt_at,
       COALESCE(last_error, ''), created_at, updated_at
FROM webhook_deliveries
WHERE claim_token = $1
ORDER BY created_at ASC;
