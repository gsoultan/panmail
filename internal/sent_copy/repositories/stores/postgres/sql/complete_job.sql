DELETE FROM sent_copy_jobs
WHERE id = $1 AND claim_token = $2
