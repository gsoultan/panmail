UPDATE outbox
SET status = $1, retry_count = $2, next_retry_at = $3, last_error = $4, updated_at = $5
WHERE id = $6;
