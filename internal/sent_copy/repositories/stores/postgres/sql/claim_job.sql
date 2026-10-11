UPDATE sent_copy_jobs
SET claim_token = $1, claimed_at = $2
WHERE id = $3 AND claim_token = $4
