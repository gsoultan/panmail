-- Clears an expired use of a key ahead of reusing it. Only an expired one: a
-- live row is exactly what the insert that follows must conflict with.
DELETE FROM send_idempotency_keys
WHERE tenant_id = $1
  AND idempotency_key = $2
  AND created_at < $3;
