-- Keys older than the window answer nothing any more.
DELETE FROM send_idempotency_keys
WHERE created_at < $1;
