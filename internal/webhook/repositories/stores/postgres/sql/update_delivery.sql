-- Releases the claim as it writes the outcome. A row left claimed after the
-- worker has finished with it is invisible until the lease expires.
--
-- Conditional on the claim token. A worker whose lease ran out mid-batch would
-- otherwise overwrite whatever the worker that reclaimed the row recorded --
-- marking a delivered notification deferred, so it is sent again.
UPDATE webhook_deliveries
SET status = $2,
    attempt_count = $3,
    next_attempt_at = $4,
    last_error = $5,
    updated_at = $6,
    claim_token = NULL,
    claimed_until = NULL
WHERE id = $1
  AND claim_token = $7;
