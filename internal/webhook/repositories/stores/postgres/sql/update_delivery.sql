-- Releases the claim as it writes the outcome. A row left claimed after the
-- worker has finished with it is invisible until the lease expires.
UPDATE webhook_deliveries
SET status = $2,
    attempt_count = $3,
    next_attempt_at = $4,
    last_error = $5,
    updated_at = $6,
    claim_token = NULL,
    claimed_until = NULL
WHERE id = $1;
