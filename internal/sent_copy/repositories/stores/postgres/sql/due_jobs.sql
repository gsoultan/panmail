SELECT id, claim_token
FROM sent_copy_jobs
WHERE next_attempt_at <= $1
  AND (claim_token = '' OR claimed_at < $2)
ORDER BY next_attempt_at
LIMIT $3
