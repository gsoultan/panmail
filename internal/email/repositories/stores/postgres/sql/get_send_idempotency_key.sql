-- The key a tenant's earlier send was accepted under, if it is still live.
-- An expired row may linger until the prune reaches it, and is not an answer.
SELECT tenant_id, idempotency_key, request_hash, message_id, created_at
FROM send_idempotency_keys
WHERE tenant_id = $1
  AND idempotency_key = $2
  AND created_at >= $3;
