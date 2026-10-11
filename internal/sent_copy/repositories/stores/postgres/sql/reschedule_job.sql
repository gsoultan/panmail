UPDATE sent_copy_jobs
SET claim_token = '', claimed_at = 0, attempts = $3, next_attempt_at = $4, last_error = $5
WHERE id = $1 AND claim_token = $2
