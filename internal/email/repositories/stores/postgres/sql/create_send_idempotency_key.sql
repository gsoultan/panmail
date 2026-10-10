-- DO NOTHING rather than an error, so a lost race reads the same on both
-- engines: no row affected. On PostgreSQL the insert waits for a concurrent
-- transaction holding the same key to finish, so by the time this returns the
-- winner's row is committed and readable.
INSERT INTO send_idempotency_keys (tenant_id, idempotency_key, request_hash, message_id, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id, idempotency_key) DO NOTHING;
